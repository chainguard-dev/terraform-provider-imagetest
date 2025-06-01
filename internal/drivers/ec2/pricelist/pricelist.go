package pricelist

import (
	_ "embed"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type _PriceList map[types.InstanceType]float32

var (
	ErrNoResults = fmt.Errorf("no instance types were.. cheapest..?")
)

func SelectCheapest(itypes []types.InstanceType) (types.InstanceType, float32) {
	if len(itypes) == 0 {
		return "", 0
	}

	cheapestIndex, cheapestPrice := 0, float32(0)
	for i := range len(itypes) {
		price, ok := PriceList[itypes[i]]
		if !ok {
			continue
		}
		if cheapestPrice == 0 || price < cheapestPrice {
			cheapestIndex = i
			cheapestPrice = price
		}
	}

	if cheapestPrice == 0 {
		return "", 0
	}

	return itypes[cheapestIndex], cheapestPrice
}
