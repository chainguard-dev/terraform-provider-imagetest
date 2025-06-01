package main

import (
	"fmt"
	"iter"
	"strings"
)

func NewStackMap(m map[string]any) *StackMap {
	const initsz = 8
	return &StackMap{
		names:   make([]string, 0, 8),
		stack:   make([]map[string]any, 0, 8),
		current: m,
	}
}

type StackMap struct {
	names   []string
	stack   []map[string]any
	current map[string]any
}

type ValueKind = string

const (
	Object = "object"
	String = "string"
)

var (
	ErrKeyNotFound = fmt.Errorf("key not found")
	ErrNotMap      = fmt.Errorf("value is not a map")
	ErrNotString   = fmt.Errorf("value is not a string")
)

func (self *StackMap) Push(key string) error {
	v, ok := self.current[key]
	if !ok {
		return ErrKeyNotFound
	}

	mv, ok := v.(map[string]any)
	if !ok {
		return ErrNotMap
	}

	self.names = append(self.names, key)
	self.stack = append(self.stack, self.current)
	self.current = mv

	return nil
}

func (self *StackMap) Pop() {
	if len(self.names) > 0 && len(self.stack) > 0 {
		self.names = self.names[:len(self.names)-1]
		self.current = self.stack[len(self.stack)-1]
		self.stack = self.stack[:len(self.stack)-1]
	}
}

func (self *StackMap) Fork(key string) (*StackMap, error) {
	v, ok := self.current[key]
	if !ok {
		return nil, ErrKeyNotFound
	}

	mv, ok := v.(map[string]any)
	if !ok {
		return nil, ErrNotMap
	}

	return &StackMap{
		names:   append(self.names, key),
		stack:   append(self.stack, self.current),
		current: mv,
	}, nil
}

func (self *StackMap) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range self.current {
			if !yield(k, v) {
				return
			}
		}
	}
}

func (self *StackMap) GetString(key string) (string, error) {
	v, ok := self.current[key]
	if !ok {
		return "", ErrKeyNotFound
	}

	sv, ok := v.(string)
	if !ok {
		return "", ErrNotString
	}

	return sv, nil
}

func (self *StackMap) Namespace() string {
	if len(self.names) == 0 {
		return "root"
	}
	return strings.Join(self.names, "::")
}

func (self *StackMap) String() string {
	return self.Namespace()
}
