package smsprovider

import "sync"

// providerRegistry maps sms_devices.device_type → configured Provider.
// Populated lazily by DefaultAEUM and future Default<X> singletons.
var (
	providerRegistry   = make(map[string]Provider)
	providerRegistryMu sync.RWMutex
)

// Register binds a Provider to its device_type. Safe for concurrent use;
// idempotent (last-write-wins) so test setups can re-register freely.
func Register(deviceType string, p Provider) {
	if deviceType == "" || p == nil {
		return
	}
	providerRegistryMu.Lock()
	providerRegistry[deviceType] = p
	providerRegistryMu.Unlock()
}

// Get returns the Provider bound to a device_type, or nil if none.
func Get(deviceType string) Provider {
	providerRegistryMu.RLock()
	defer providerRegistryMu.RUnlock()
	return providerRegistry[deviceType]
}

// ResetRegistryForTest empties the registry. Tests-only.
func ResetRegistryForTest() {
	providerRegistryMu.Lock()
	providerRegistry = make(map[string]Provider)
	providerRegistryMu.Unlock()
}
