package ec2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSingleAddrCidr(t *testing.T) {
	const ipv4Addr = "172.16.32.254"
	const ipv6Addr = "2404:6800:4009:805::200e"
	const invalidIP = "172.16.32.256"

	res, err := singleAddrCIDR(ipv4Addr)
	assert.Equal(t, ipv4Addr+"/32", res)
	assert.NoError(t, err)

	res, err = singleAddrCIDR(ipv6Addr)
	assert.Equal(t, ipv6Addr+"/128", res)
	assert.NoError(t, err)

	res, err = singleAddrCIDR(invalidIP)
	assert.Equal(t, "", res)
	assert.Error(t, err)
}
