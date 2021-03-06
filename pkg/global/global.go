package global

import "sync"

var registry sync.Map

func Store(key string, ref interface{}) {
	registry.Store(key, ref)
}

func Get(key string) (interface{}, bool) {
	v, ok := registry.Load(key)
	return v, ok
}
