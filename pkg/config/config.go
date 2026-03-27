package config

import "os"

// Default ports for each core service.
const (
	DefaultOrchestratorPort = "19080"
	DefaultRegistryPort     = "19081"
	DefaultPolicyEnginePort = "19082"
)

// OrchestratorPort returns the listen address for the orchestrator service.
// Override with the ORCHESTRATOR_PORT environment variable.
func OrchestratorPort() string {
	return listenAddr("ORCHESTRATOR_PORT", DefaultOrchestratorPort)
}

// RegistryPort returns the listen address for the registry service.
// Override with the REGISTRY_PORT environment variable.
func RegistryPort() string {
	return listenAddr("REGISTRY_PORT", DefaultRegistryPort)
}

// PolicyEnginePort returns the listen address for the policy engine service.
// Override with the POLICY_ENGINE_PORT environment variable.
func PolicyEnginePort() string {
	return listenAddr("POLICY_ENGINE_PORT", DefaultPolicyEnginePort)
}

func listenAddr(envKey, defaultPort string) string {
	if v := os.Getenv(envKey); v != "" {
		return ":" + v
	}
	return ":" + defaultPort
}
