package environment

import (
	"net"
	"sync"
)

type PortAllocator struct {
	ports map[int]bool
	mu    sync.Mutex
}

func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		ports: make(map[int]bool),
		mu:    sync.Mutex{},
	}
}

type ClaimedPort struct {
	Free func()
	Port int
}

func (p *PortAllocator) Allocate() (*ClaimedPort, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, err
		}
		defer l.Close()

		ta, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			return nil, err
		}

		if p.ports[ta.Port] {
			continue
		}

		p.ports[ta.Port] = true

		free := func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			delete(p.ports, ta.Port)
		}

		return &ClaimedPort{
			Port: ta.Port,
			Free: free,
		}, nil
	}
}
