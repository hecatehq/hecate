package providers

import "sync"

type MutableRegistry struct {
	mu        sync.RWMutex
	instances []ProviderInstance
	byName    map[string]ProviderInstance
}

func NewMutableRegistry(items ...Provider) *MutableRegistry {
	registry := &MutableRegistry{}
	registry.Replace(items...)
	return registry
}

func (r *MutableRegistry) Replace(items ...Provider) {
	byName := make(map[string]ProviderInstance, len(items))
	instances := make([]ProviderInstance, 0, len(items))
	for _, provider := range items {
		instance := newProviderInstance(provider)
		byName[provider.Name()] = instance
		instances = append(instances, instance)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName = byName
	r.instances = instances
}

func (r *MutableRegistry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, ok := r.byName[name]
	return instance.Provider, ok
}

func (r *MutableRegistry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.instances))
	for _, instance := range r.instances {
		out = append(out, instance.Provider)
	}
	return out
}

func (r *MutableRegistry) GetInstance(name string) (ProviderInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, ok := r.byName[name]
	return instance, ok
}

func (r *MutableRegistry) AllInstances() []ProviderInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderInstance, len(r.instances))
	copy(out, r.instances)
	return out
}
