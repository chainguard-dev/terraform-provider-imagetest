package envs

import (
	"context"
	"net"
	"sync"
)

type Ports struct {
	ports map[int]bool
	mu    sync.Mutex
}

func NewFreePort() *Ports {
	return &Ports{
		ports: make(map[int]bool),
		mu:    sync.Mutex{},
	}
}

type FreePort func()

func (p *Ports) Get(ctx context.Context) (int, FreePort, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return 0, nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return 0, nil, err
		}
		defer l.Close()

		ta, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			return 0, nil, err
		}

		if p.ports[ta.Port] {
			continue
		}

		p.ports[ta.Port] = true
		return ta.Port, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			delete(p.ports, ta.Port)
		}, nil
	}
}
