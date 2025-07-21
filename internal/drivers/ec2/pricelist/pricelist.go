package pricelist

import (
	"bytes"
	_ "embed"
	"fmt"
	"reflect"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

//go:generate go tool plgen --package-name pricelist --package-path . --file-name prices.go
type PriceList map[types.InstanceType]float32

var _ fmt.GoStringer = (*PriceList)(nil)

// GoString implements 'fmt.GoStringer' for the purposes of stringifying itself
// during codegen (see: 'cmd/plgen').
func (self PriceList) GoString() string {
	// A linter on this repository expects 'go:generate'd content to be committed
	// and the 'go:generate'd output _in CI_ to match what was committed. This
	// means we have to sort this slice.
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

// Lookup simply fetches the price of a provided EC2 instance type.
func Lookup(instanceType types.InstanceType) (float32, bool) {
	price, ok := priceList[instanceType]
	return price, ok
}

// SelectCheapest reviews the cost of each provided instance type, returning
// the cheapest of the bunch along with its per-hour (on-demand) cost.
func SelectCheapest(instanceTypes []types.InstanceType) (types.InstanceType, float32) {
	if len(instanceTypes) == 0 {
		return "", 0
	}
	var cheapestInstanceType types.InstanceType
	var cheapestPrice float32
	for _, instanceType := range instanceTypes {
		price, ok := priceList[instanceType]
		if ok && (cheapestPrice == 0 || price < cheapestPrice) {
			cheapestInstanceType = instanceType
			cheapestPrice = price
		}
	}
	if cheapestPrice == 0 {
		return "", 0
	}
	return cheapestInstanceType, cheapestPrice
}
