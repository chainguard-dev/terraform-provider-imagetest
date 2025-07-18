package pricelist

import (
	"bytes"
	_ "embed"
	"fmt"
	"reflect"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var _ fmt.GoStringer = (*PriceList)(nil)

//go:generate go tool plgen --package-name pricelist --package-path . --file-name prices.go
type PriceList map[types.InstanceType]float32

func (self PriceList) GoString() string {
	// A linter on this repository expects `go:generate`d content to be committed
	// and the `go:generate`d output _in CI_ to match what was committed. This
	// means we have to sort this slice, for no reason than to satisfy the linter
	// -_-
	keys := make([]types.InstanceType, len(self))
	i := 0
	for k := range self {
		keys[i] = k
		i += 1
	}
	slices.Sort(keys)
	// Now generate the output
	b := bytes.NewBuffer(make([]byte, 0, 1024*50))
	fmt.Fprintln(b, reflect.TypeOf(self).Name(), "{")
	for _, k := range keys {
		fmt.Fprintf(b, "\t%q: %.2f,\n", k, self[k])
	}
	fmt.Fprintln(b, "}")
	return b.String()
}

var ErrNoResults = fmt.Errorf("no instance types were.. cheapest..?")

func Lookup(instanceType types.InstanceType) (float32, bool) {
	price, ok := priceList[instanceType]
	return price, ok
}

func SelectCheapest(itypes []types.InstanceType) (types.InstanceType, float32) {
	if len(itypes) == 0 {
		return "", 0
	}

	cheapestIndex, cheapestPrice := 0, float32(0)
	for i := range len(itypes) {
		price, ok := priceList[itypes[i]]
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
