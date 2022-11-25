package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apparentlymart/go-cidr/cidr"
	"github.com/kelseyhightower/envconfig"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"
	ctrl "k8s.io/kubernetes/pkg/controlplane"
	"sigs.k8s.io/yaml"

	"github.com/openshift/microshift/pkg/util"
)

const (
	defaultUserConfigFile   = "~/.microshift/config.yaml"
	defaultUserDataDir      = "~/.microshift/data"
	defaultGlobalConfigFile = "/etc/microshift/config.yaml"
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

type ClusterConfig struct {
	URL string `json:"url"`

	ClusterCIDR          string `json:"clusterCIDR"`
	ServiceCIDR          string `json:"serviceCIDR"`
	ServiceNodePortRange string `json:"serviceNodePortRange"`
	DNS                  string `json:"-"`
	Domain               string `json:"domain"`
}

type IngressConfig struct {
	ServingCertificate []byte
	ServingKey         []byte
}

type MicroshiftConfig struct {
	LogVLevel int `json:"logVLevel"`

	SubjectAltNames []string `json:"subjectAltNames"`
	NodeName        string   `json:"nodeName"`
	NodeIP          string   `json:"nodeIP"`

	Cluster ClusterConfig `json:"cluster"`

	Ingress IngressConfig `json:"-"`
}

func GetConfigFile() string {
	return configFile
}

func GetDataDir() string {
	return dataDir
}

func GetManifestsDir() []string {
	return manifestsDir
}

// KubeConfigID identifies the different kubeconfigs managed in the DataDir
type KubeConfigID string

const (
	KubeAdmin             KubeConfigID = "kubeadmin"
	KubeControllerManager KubeConfigID = "kube-controller-manager"
	KubeScheduler         KubeConfigID = "kube-scheduler"
	Kubelet               KubeConfigID = "kubelet"
)

// KubeConfigPath returns the path to the specified kubeconfig file.
func (cfg *MicroshiftConfig) KubeConfigPath(id KubeConfigID) string {
	return filepath.Join(dataDir, "resources", string(id), "kubeconfig")
}

func (cfg *MicroshiftConfig) KubeConfigAdminPath(id string) string {
	return filepath.Join(dataDir, "resources", string(KubeAdmin), id, "kubeconfig")
}

func getAllHostnames() ([]string, error) {
	cmd := exec.Command("/bin/hostname", "-A")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("Error when executing 'hostname -A': %v", err)
	}
	outString := out.String()
	outString = strings.Trim(outString[:len(outString)-1], " ")
	// Remove duplicates to avoid having them in the certificates.
	names := strings.Split(outString, " ")
	set := sets.NewString(names...)
	return set.List(), nil
}

func NewMicroshiftConfig() *MicroshiftConfig {
	nodeName, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Failed to get hostname %v", err)
	}
	nodeIP, err := util.GetHostIP()
	if err != nil {
		klog.Fatalf("failed to get host IP: %v", err)
	}
	subjectAltNames, err := getAllHostnames()
	if err != nil {
		klog.Fatalf("failed to get all hostnames: %v", err)
	}

	return &MicroshiftConfig{
		LogVLevel:       0,
		SubjectAltNames: subjectAltNames,
		NodeName:        nodeName,
		NodeIP:          nodeIP,
		Cluster: ClusterConfig{
			URL:                  "https://127.0.0.1:6443",
			ClusterCIDR:          "10.42.0.0/16",
			ServiceCIDR:          "10.43.0.0/16",
			ServiceNodePortRange: "30000-32767",
			Domain:               "cluster.local",
		},
	}
}

// extract the api server port from the cluster URL
func (c *ClusterConfig) ApiServerPort() (int, error) {
	var port string

	parsed, err := url.Parse(c.URL)
	if err != nil {
		return 0, err
	}

	// default empty URL to port 6443
	port = parsed.Port()
	if port == "" {
		port = "6443"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return 0, err
	}
	return portNum, nil
}

// Returns the default user config file if that exists, else the default global
// config file, else the empty string.
func findConfigFile() string {
	userConfigFile, _ := homedir.Expand(defaultUserConfigFile)
	if _, err := os.Stat(userConfigFile); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(defaultGlobalConfigFile); errors.Is(err, os.ErrNotExist) {
			return ""
		} else {
			return defaultGlobalConfigFile
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

func StringInList(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (c *MicroshiftConfig) ReadFromConfigFile(configFile string) error {
	contents, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("reading config file %q: %v", configFile, err)
	}

	if err := yaml.Unmarshal(contents, c); err != nil {
		return fmt.Errorf("decoding config file %q: %v", configFile, err)
	}

	return nil
}

func (c *MicroshiftConfig) ReadFromEnv() error {
	if err := envconfig.Process("microshift", c); err != nil {
		return err
	}
	return nil
}

func (c *MicroshiftConfig) ReadFromCmdLine(flags *pflag.FlagSet) error {
	if f := flags.Lookup("v"); f != nil && flags.Changed("v") {
		c.LogVLevel, _ = strconv.Atoi(f.Value.String())
	}
	if s, err := flags.GetStringSlice("subject-alt-names"); err == nil && flags.Changed("subject-alt-names") {
		c.SubjectAltNames = s
	}
	if s, err := flags.GetString("node-name"); err == nil && flags.Changed("node-name") {
		c.NodeName = s
	}
	if s, err := flags.GetString("node-ip"); err == nil && flags.Changed("node-ip") {
		c.NodeIP = s
	}
	if s, err := flags.GetString("url"); err == nil && flags.Changed("url") {
		c.Cluster.URL = s
	}
	if s, err := flags.GetString("cluster-cidr"); err == nil && flags.Changed("cluster-cidr") {
		c.Cluster.ClusterCIDR = s
	}
	if s, err := flags.GetString("service-cidr"); err == nil && flags.Changed("service-cidr") {
		c.Cluster.ServiceCIDR = s
	}
	if s, err := flags.GetString("service-node-port-range"); err == nil && flags.Changed("service-node-port-range") {
		c.Cluster.ServiceNodePortRange = s
	}
	if s, err := flags.GetString("cluster-domain"); err == nil && flags.Changed("cluster-domain") {
		c.Cluster.Domain = s
	}

	return nil
}

// Note: add a configFile parameter here because of unit test requiring custom
// local directory
func (c *MicroshiftConfig) ReadAndValidate(configFile string, flags *pflag.FlagSet) error {
	if configFile != "" {
		if err := c.ReadFromConfigFile(configFile); err != nil {
			return err
		}
	}
	if err := c.ReadFromEnv(); err != nil {
		return err
	}
	if err := c.ReadFromCmdLine(flags); err != nil {
		return err
	}

	// validate serviceCIDR
	clusterDNS, err := getClusterDNS(c.Cluster.ServiceCIDR)
	if err != nil {
		klog.Fatalf("failed to get DNS IP: %v", err)
	}
	c.Cluster.DNS = clusterDNS

	if len(c.SubjectAltNames) > 0 {
		// Any entry in SubjectAltNames will be included in the external access certificates.
		// Any of the hostnames and IPs (except the node IP) listed below conflicts with
		// other certificates, such as the service network and localhost access.
		// The node IP is a bit special. Apiserver k8s service, which holds a service IP
		// gets resolved to the node IP. If we include the node IP in the SAN then we have
		// an ambiguity, the same IP matches two different certificates and there are errors
		// when trying to reach apiserver from within the cluster using the service IP.
		// Apiserver will decide which certificate to return to client hello based on SNI
		// (which client-go does not use) or raw IP mappings. As soon as there is a match for
		// the node IP it returns that certificate, which is the external access one. This
		// breaks all pods trying to reach apiserver, as hostnames dont match and the certificate
		// is invalid.
		if stringSliceContains(c.SubjectAltNames, "localhost", "127.0.0.1", c.NodeIP) {
			klog.Fatal("subjectAltNames must not contain localhost, 127.0.0.1 or node IP")
		}

		// unchecked error because this was done when getting cluster DNS
		_, svcNet, _ := net.ParseCIDR(c.Cluster.ServiceCIDR)
		_, apiServerServiceIP, err := ctrl.ServiceIPRange(*svcNet)
		if err != nil {
			klog.Fatalf("error getting apiserver IP: %v", err)
		}
		if stringSliceContains(
			c.SubjectAltNames,
			"kubernetes",
			"kubernetes.default",
			"kubernetes.default.svc",
			"kubernetes.default.svc.cluster.local",
			"openshift",
			"openshift.default",
			"openshift.default.svc",
			"openshift.default.svc.cluster.local",
			apiServerServiceIP.String(),
		) {
			klog.Fatal("subjectAltNames must not contain apiserver kubernetes service names or IPs")
		}
	}

	u, err := url.Parse(c.Cluster.URL)
	if err != nil {
		klog.Fatalf("failed to parse cluster URL: %v", err)
	}
	if !stringSliceContains(c.SubjectAltNames, u.Host) || u.Host != c.NodeName {
		klog.Fatal("Cluster URL is using a host not included in subjectAltNames or nodeName")
	}

	return nil
}

// getClusterDNS returns cluster DNS IP that is 10th IP of the ServiceNetwork
func getClusterDNS(serviceCIDR string) (string, error) {
	_, service, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid service cidr %v: %v", serviceCIDR, err)
	}
	dnsClusterIP, err := cidr.Host(service, 10)
	if err != nil {
		return "", fmt.Errorf("service cidr must have at least 10 distinct host addresses %v: %v", serviceCIDR, err)
	}

	return dnsClusterIP.String(), nil
}

func stringSliceContains(list []string, elements ...string) bool {
	for _, value := range list {
		for _, element := range elements {
			if value == element {
				return true
			}
		}
	}
	return false
}

func HideUnsupportedFlags(flags *pflag.FlagSet) {
	// hide logging flags that we do not use/support
	loggingFlags := pflag.NewFlagSet("logging-flags", pflag.ContinueOnError)
	logs.AddFlags(loggingFlags)

	supportedLoggingFlags := sets.NewString("v")

	loggingFlags.VisitAll(func(pf *pflag.Flag) {
		if !supportedLoggingFlags.Has(pf.Name) {
			flags.MarkHidden(pf.Name)
		}
	})

	flags.MarkHidden("version")
}
