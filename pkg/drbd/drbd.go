package drbd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	resourceConfigDir = "/etc/drbd.d"
	resourceTemplate  = `resource {{.Name}} {
    protocol {{.Protocol}};
    device /dev/drbd{{.Minor}};
    disk /dev/{{.VGName}}/{{.Name}};
    meta-disk internal;
    on {{.LocalNodeName}} {
        address {{.LocalAddr}}:{{.Port}};
        node-id 0;
    }
    on {{.RemoteNodeName}} {
        address {{.RemoteAddr}}:{{.Port}};
        node-id 1;
    }
    net {
        after-sb-0pri discard-zero-changes;
        after-sb-1pri discard-secondary;
        after-sb-2pri disconnect;
    }
}
`
)

type ResourceConfig struct {
	Name           string
	Protocol       string
	Minor          int
	Port           int
	VGName         string
	LocalNodeName  string
	LocalAddr      string
	RemoteNodeName string
	RemoteAddr     string
}

type Status struct {
	Name            string
	Role            string // Primary or Secondary
	ConnectionState string // Connected, Connecting, StandAlone, etc.
	DiskState       string // UpToDate, Inconsistent, Diskless, etc.
	PeerDiskState   string
}

// drbdsetup status output JSON structures
type drbdStatusReport []drbdResource

type drbdResource struct {
	Name        string           `json:"name"`
	Role        string           `json:"role"`
	Devices     []drbdDevice     `json:"devices"`
	Connections []drbdConnection `json:"connections"`
}

type drbdDevice struct {
	Volume    int    `json:"volume"`
	Minor     int    `json:"minor"`
	DiskState string `json:"disk-state"`
}

type drbdConnection struct {
	PeerNodeID  int              `json:"peer-node-id"`
	Name        string           `json:"name"`
	ConnState   string           `json:"connection-state"`
	PeerRole    string           `json:"peer-role"`
	PeerDevices []drbdPeerDevice `json:"peer_devices"`
}

type drbdPeerDevice struct {
	Volume        int    `json:"volume"`
	PeerDiskState string `json:"peer-disk-state"`
}

// WriteResourceConfig writes a DRBD resource configuration file.
func WriteResourceConfig(log *slog.Logger, cfg ResourceConfig) error {
	tmpl, err := template.New("drbd-resource").Parse(resourceTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse drbd resource template: %w", err)
	}

	configPath := filepath.Join(resourceConfigDir, cfg.Name+".res")
	log.Info("writing drbd resource config", "path", configPath)

	f, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create drbd resource config %s: %w", configPath, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, cfg); err != nil {
		return fmt.Errorf("failed to render drbd resource config: %w", err)
	}

	return nil
}

// RemoveResourceConfig deletes the DRBD resource configuration file.
func RemoveResourceConfig(log *slog.Logger, name string) error {
	configPath := filepath.Join(resourceConfigDir, name+".res")
	log.Info("removing drbd resource config", "path", configPath)

	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove drbd resource config %s: %w", configPath, err)
	}
	return nil
}

// CreateMetadata initializes DRBD metadata on the backing device.
func CreateMetadata(log *slog.Logger, name string) (string, error) {
	log.Info("creating drbd metadata", "resource", name)
	cmd := exec.Command("drbdadm", "create-md", "--force", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to create drbd metadata for %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// Up brings up the DRBD resource (connects and attaches).
func Up(log *slog.Logger, name string) (string, error) {
	log.Info("bringing up drbd resource", "resource", name)
	cmd := exec.Command("drbdadm", "up", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to bring up drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// Down tears down the DRBD resource (disconnects and detaches).
func Down(log *slog.Logger, name string) (string, error) {
	log.Info("bringing down drbd resource", "resource", name)
	cmd := exec.Command("drbdadm", "down", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to bring down drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// Promote makes this node the primary for the DRBD resource.
func Promote(log *slog.Logger, name string) (string, error) {
	log.Info("promoting drbd resource to primary", "resource", name)
	cmd := exec.Command("drbdadm", "primary", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to promote drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// ForcePromote makes this node the primary even without a peer.
func ForcePromote(log *slog.Logger, name string) (string, error) {
	log.Info("force-promoting drbd resource to primary", "resource", name)
	cmd := exec.Command("drbdadm", "primary", "--force", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to force-promote drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// Demote makes this node the secondary for the DRBD resource.
func Demote(log *slog.Logger, name string) (string, error) {
	log.Info("demoting drbd resource to secondary", "resource", name)
	cmd := exec.Command("drbdadm", "secondary", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to demote drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// InitialSync triggers initial full sync from this node (must be primary).
func InitialSync(log *slog.Logger, name string) (string, error) {
	log.Info("starting initial drbd sync (new-current-uuid --clear-bitmap)", "resource", name)
	cmd := exec.Command("drbdadm", "--", "--overwrite-data-of-peer", "primary", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to start initial sync for %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// Resize notifies DRBD that the backing device has changed size.
func Resize(log *slog.Logger, name string) (string, error) {
	log.Info("resizing drbd resource", "resource", name)
	cmd := exec.Command("drbdadm", "resize", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to resize drbd resource %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// WipeMetadata removes DRBD metadata from the backing device.
func WipeMetadata(log *slog.Logger, name string) (string, error) {
	log.Info("wiping drbd metadata", "resource", name)
	cmd := exec.Command("drbdadm", "wipe-md", "--force", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("failed to wipe drbd metadata for %s: %w (%s)", name, err, string(out))
	}
	return string(out), nil
}

// GetStatus returns the DRBD status for a resource.
func GetStatus(log *slog.Logger, name string) (*Status, error) {
	cmd := exec.Command("drbdsetup", "status", name, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Resource may not be up yet
		if strings.Contains(string(out), "No such resource") {
			return &Status{
				Name:            name,
				Role:            "Unknown",
				ConnectionState: "Unknown",
				DiskState:       "Unknown",
				PeerDiskState:   "Unknown",
			}, nil
		}
		return nil, fmt.Errorf("failed to get drbd status for %s: %w (%s)", name, err, string(out))
	}

	var report drbdStatusReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("failed to parse drbd status output: %w", err)
	}

	for _, res := range report {
		if res.Name != name {
			continue
		}

		status := &Status{
			Name: name,
			Role: res.Role,
		}

		if len(res.Devices) > 0 {
			status.DiskState = res.Devices[0].DiskState
		}

		if len(res.Connections) > 0 {
			status.ConnectionState = res.Connections[0].ConnState
			if len(res.Connections[0].PeerDevices) > 0 {
				status.PeerDiskState = res.Connections[0].PeerDevices[0].PeerDiskState
			}
		}

		return status, nil
	}

	return nil, fmt.Errorf("resource %s not found in drbd status output", name)
}

// ResourceExists checks if a DRBD resource config file exists.
func ResourceExists(name string) bool {
	configPath := filepath.Join(resourceConfigDir, name+".res")
	_, err := os.Stat(configPath)
	return err == nil
}

// DevicePath returns the DRBD device path for a given minor number.
func DevicePath(minor int) string {
	return fmt.Sprintf("/dev/drbd%d", minor)
}
