package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/mitchellh/go-homedir"
	"sigs.k8s.io/yaml"
)

const (
	DefaultUserConfigFile   = "~/.microshift/config.yaml"
	defaultUserDataDir      = "~/.microshift/data"
	DefaultGlobalConfigFile = "/etc/microshift/config.yaml"
	defaultGlobalDataDir    = "/var/lib/microshift"
	// for files managed via management system in /etc, i.e. user applications
	defaultManifestDirEtc = "/etc/microshift/manifests"
	// for files embedded in ostree. i.e. cni/other component customizations
	defaultManifestDirLib = "/usr/lib/microshift/manifests"
)

var (
	configFile   = findConfigFile()
	dataDir      = findDataDir()
	manifestsDir = findManifestsDir()
)

func GetConfigFile() string {
	return configFile
}

func GetDataDir() string {
	return dataDir
}

func GetManifestsDir() []string {
	return manifestsDir
}

// Returns the default user config file if that exists, else the default global
// config file, else the empty string.
func findConfigFile() string {
	userConfigFile, _ := homedir.Expand(DefaultUserConfigFile)
	if _, err := os.Stat(userConfigFile); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(DefaultGlobalConfigFile); errors.Is(err, os.ErrNotExist) {
			return ""
		} else {
			return DefaultGlobalConfigFile
		}
	} else {
		return userConfigFile
	}
}

// Returns the default user data dir if it exists or the user is non-root.
// Returns the default global data dir otherwise.
func findDataDir() string {
	userDataDir, _ := homedir.Expand(defaultUserDataDir)
	if _, err := os.Stat(userDataDir); errors.Is(err, os.ErrNotExist) {
		if os.Geteuid() > 0 {
			return userDataDir
		} else {
			return defaultGlobalDataDir
		}
	} else {
		return userDataDir
	}
}

// Returns the default manifests directories
func findManifestsDir() []string {
	var manifestsDir = []string{defaultManifestDirLib, defaultManifestDirEtc}
	return manifestsDir
}

func parse(contents []byte) (*Config, error) {
	c := &Config{}
	if err := yaml.Unmarshal(contents, c); err != nil {
		return nil, fmt.Errorf("Unable to decode configuration: %v", err)
	}
	return c, nil
}

func getActiveConfigFromYAML(contents []byte) (*Config, error) {
	userSettings, err := parse(contents)
	if err != nil {
		return nil, fmt.Errorf("Error parsing config file %q: %v", configFile, err)
	}

	// Start with the defaults, then apply the user settings and
	// recompute dynamic values.
	results := &Config{}
	if err := results.fillDefaults(); err != nil {
		return nil, fmt.Errorf("Invalid configuration: %v", err)
	}
	results.incorporateUserSettings(userSettings)
	if err := results.updateComputedValues(); err != nil {
		return nil, fmt.Errorf("Invalid configuration: %v", err)
	}
	if err := results.validate(); err != nil {
		return nil, fmt.Errorf("Invalid configuration: %v", err)
	}
	return results, nil
}

// ActiveConfig returns the active configuration. If the configuration
// file exists, read it and require it to be valid. Otherwise return
// the default settings.
func ActiveConfig() (*Config, error) {
	filename := GetConfigFile()
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		// No configuration file, use the default settings
		return NewDefault(), nil
	} else if err != nil {
		return nil, err
	}

	// Read the file and merge user-provided settings with the defaults
	contents, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("Error reading config file %q: %v", configFile, err)
	}
	return getActiveConfigFromYAML(contents)
}
