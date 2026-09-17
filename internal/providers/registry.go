package providers

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrProviderAlreadyRegistered = errors.New("provider type already registered")
	ErrUnknownProvider           = errors.New("unknown provider type")
)

type Registry struct {
	mutex     sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (registry *Registry) Register(providerType string, factory Factory) error {
	providerType = strings.TrimSpace(strings.ToLower(providerType))
	if providerType == "" || factory == nil {
		return errors.New("provider type and factory are required")
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.factories[providerType]; exists {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyRegistered, providerType)
	}
	registry.factories[providerType] = factory
	return nil
}

func (registry *Registry) Create(config ConnectionConfig) (Provider, error) {
	providerType := strings.TrimSpace(strings.ToLower(config.Type))
	registry.mutex.RLock()
	factory, exists := registry.factories[providerType]
	registry.mutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, providerType)
	}
	config.Type = providerType
	return factory.Create(config)
}

func (registry *Registry) Types() []string {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	types := make([]string, 0, len(registry.factories))
	for providerType := range registry.factories {
		types = append(types, providerType)
	}
	sort.Strings(types)
	return types
}
