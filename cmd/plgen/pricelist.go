package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/pricelist"
)

type (
	// PriceList reflects the shape of the JSON price list returned by the public
	// AWS API and that data structure sucks to work with...
	//
	// What we're after is the USD value for all price dimensions we care about.
	// In JSONPath terms, what we want looks roughly like this:
	//   .terms.OnDemand.*.*.priceDimensions.*.pricePerUnit.USD
	//                   ^ ^                 ^
	//                   | |                 |
	//                   | |                 |- [SKU].[OFFER_TERM_CODE].[RATE_CODE]
	//                   | |- [SKU].[OFFER_TERM_CODE]
	//                   |- [SKU]
	//
	// The difficulty is all objects under the '.terms' top-level object contain
	// zero information about the instance (vCPUs, etc.) We have to get those
	// things from the '.products' object. In JSONPath terms, to lookup what we
	// want in the '.terms' side, we need these:
	//   .products.*.productFamily
	//   .products.*.productFamily.attributes | .instanceType, .tenancy, .usagetype, .operatingSystem, .preInstalledSw
	//             ^                             ^----------^   ^-----^   ^-------^   ^-------------^   ^------------^
	//             |                             |              |         |           |                 |
	//             |                             |              |         |           |                 |- We want NO preinstalled SW.
	//             |                             |              |         |           |- We want _only_ Linux.
	//             |                             |              |         |- We want only the '-BoxUsage:' type.
	//             |                             |              |- We want only 'Shared' tenancy.
	//             |                             |- We want to record the instance type (ex: 't2.large').
	//             |- [SKU] - We'll use this to index into the '.terms' object (above).
	PriceList struct {
		Version  string   `json:"version"`
		Products Products `json:"products"`
		Terms    Terms    `json:"terms"`
	}

	Products map[SKU]Product
	Product  struct {
		Attributes ProductAttributes `json:"attributes"`
	}
	ProductAttributes struct {
		InstanceType   string `json:"instanceType"`
		Tenancy        string `json:"tenancy"`
		UsageType      string `json:"usagetype"`
		OS             string `json:"operatingSystem"`
		PreinstalledSW string `json:"preInstalledSw"`
	}
	SKU = string

	Terms     map[Term]Offers
	Offers    map[SKU]Offer
	Offer     map[OfferCode]OfferTerm
	OfferTerm struct {
		PriceDimensions map[PriceDimensionCode]PriceDimension
	}
	PriceDimension struct {
		PricePerUnit PricePerUnit `json:"pricePerUnit"`
	}
	PricePerUnit struct {
		USD string
	}
	OfferCode          = string
	Term               = string
	PriceDimensionCode = string

	ProductFilter func(p Product) bool
)

const (
	// Regions.
	RegionUSW2 = "us-west-2"

	// Product Codes.
	ProductCodeEC2 = "AmazonEC2"
)

// priceListFetch fetches and deserialies the price list from the
// unauthenticated AWS public endpoint.
func priceListFetch(ctx context.Context, product, region string) (PriceList, error) {
	// The base URL format we're going to query for the price list
	//
	// NOTE: The subdomain segment of 'us-east-1' is just the region that will
	// actually serve us the price list, not the region the price list is for.
	const urlFmt = "https://pricing.us-east-1.amazonaws.com" +
		"/offers/v1.0/aws/" +
		"%s" /* Product Code (ex: AmazonEC2) */ +
		"/current/" +
		"%s" /* Region */ +
		"/index.json"

	// Set defaults
	if product == "" {
		product = ProductCodeEC2
	}
	if region == "" {
		region = RegionUSW2
	}

	// Format the URL
	url := fmt.Sprintf(urlFmt, product, region)

	// Init the request
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PriceList{}, fmt.Errorf("failed to init HTTP GET request: %w", err)
	}

	// Fetch the response
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return PriceList{}, fmt.Errorf("failed to perform HTTP GET request: %w", err)
	}
	defer res.Body.Close()

	// Decode the response body
	var pl PriceList
	if err = json.NewDecoder(res.Body).Decode(&pl); err != nil {
		return PriceList{}, fmt.Errorf("failed to decode pricelist response: %w", err)
	}

	return pl, nil
}

// priceListProductFilter iterates all provided PriceList 'pl's Products,
// applying all provided filters (logically ANDed). Any Products which do not
// satisfy all filters will be deleted from the map. The filtered PriceList is
// returned.
func priceListProductFilter(pl PriceList, filters ...ProductFilter) PriceList {
	for sku, product := range pl.Products {
		for _, filter := range filters {
			if !filter(product) {
				delete(pl.Products, sku)
				break
			}
		}
	}
	return pl
}

// priceListConvert iterates provided PriceList 'pl's Products, fetching
// related pricing information from the 'Terms' object and transforming it
// into an internal/drivers/ec2/pricelist.PriceList.
func priceListConvert(pl PriceList) (pricelist.PriceList, error) {
	const termsKeyOnDemand = "OnDemand"
	log := slog.Default()

	// Fetch the 'OnDemand' terms key - this is the only pricing information we
	// care about currently
	onDemandTerms, ok := pl.Terms[termsKeyOnDemand]
	if !ok {
		return nil, fmt.Errorf("price list (map) has no key [%s]", termsKeyOnDemand)
	}

	// Init the generated map
	generated := make(pricelist.PriceList)

	// Iterate all products and correlate their SKUs to their prices
	for sku, product := range pl.Products {
		// Collect the key we'll use to index into the generated map
		key := product.Attributes.InstanceType
		// Collect the instance type's price per hour
		//
		// Lookup the SKU's Offers
		offers, ok := onDemandTerms[sku]
		log := log.With("sku", sku, "instance_type", key)
		if !ok {
			log.Warn("failed to locate offers for specified SKU")
			continue
		}
		// Iterate over 'offers' (map)
		for _, offerTerm := range offers {
			// Iterate over 'priceDimensions' (map)
			for _, priceDimension := range offerTerm.PriceDimensions {
				// Get the USD price
				priceStr := priceDimension.PricePerUnit.USD
				// Parse as float32
				price, err := strconv.ParseFloat(priceStr, 32)
				if err != nil {
					log.Error(
						"failed to parse offer price per-unit as a float32",
						"input", priceStr,
						"error", err,
					)
					continue
				}
				// Ignore zero-dollar prices
				if price == 0 {
					log.Debug("skipping zero-dollar price")
					continue
				}
				// Check if the map key is already set - this shouldn't happen as the
				// only duplicate values we should expect are zero-dollar values, which
				// we skip over just above
				if current, ok := generated[types.InstanceType(key)]; ok && current != 0 {
					log.Error(
						"key already set for instance type",
						"current", current,
						"new", float32(price),
					)
					continue
				}
				// Set the map key
				generated[types.InstanceType(key)] = float32(price)
			}
		}
	}

	return generated, nil
}
