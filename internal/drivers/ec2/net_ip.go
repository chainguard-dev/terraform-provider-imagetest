package ec2

import (
	"fmt"
	"io"
	"net"
	"net/http"
)

var ErrPublicIPLookup = fmt.Errorf("failed to resolve public IP address")

// publicAddr simply returns your public IP address!
//
// Part of this whole workflow involves exposing ports (namely SSH) from the EC2
// instance. To make this reasonably secure, we restrict these port rules to
// only allow connections from the public IP address of the calling system.
func publicAddr() (string, error) {
	// TODO: We probably want a Chainguard echo service for this?
	const provider = "https://api.ipify.org"
	res, err := http.Get(provider)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
	} else if res.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("%w: received HTTP status code %d", ErrPublicIPLookup, res.StatusCode)
	}
	defer func() {
		_ = res.Body.Close()
	}()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
	}
	return string(data), nil
}

var ErrPublicIPCheck = fmt.Errorf("failed to determine public IP address")

func singleAddrCIDR(addr string) (string, error) {
	family, err := addrFamily(addr)
	if err != nil {
		return "", err // No annotation required.
	}
	switch family {
	case IPv4:
		return fmt.Sprintf("%s/32", addr), nil
	case IPv6:
		return fmt.Sprintf("%s/128", addr), nil
	default:
		return "", ErrPublicIPCheck
	}
}

var ErrAddressInvalid = fmt.Errorf("failed to parse provided IP address")

type AddressFamily uint8

const (
	IPv4 AddressFamily = 1 + iota
	IPv6
)

func addrFamily(addr string) (AddressFamily, error) {
	ip := net.ParseIP(addr)
	if ip.DefaultMask() != nil {
		return IPv4, nil
	} else if v6 := ip.To16(); v6 != nil {
		return IPv6, nil
	} else {
		return 0, ErrAddressInvalid
	}
}
