package pricelist

import (
	"bytes"
	_ "embed"
	"fmt"
	"reflect"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var _ fmt.GoStringer = (*PriceList)(nil)

type PriceList map[types.InstanceType]float32

func (self PriceList) GoString() string {
	b := bytes.NewBuffer(make([]byte, 0, 1024*50))
	fmt.Fprintln(b, reflect.TypeOf(self).Name(), "{")
	for instanceType, perHourCost := range self {
		fmt.Fprintf(b, "\t%q: %.2f,\n", instanceType, perHourCost)
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
