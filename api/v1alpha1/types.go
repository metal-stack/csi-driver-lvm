package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRBDVolumeSpec defines the desired state of a DRBD-replicated volume.
type DRBDVolumeSpec struct {
	// VolumeName is the CSI volume ID (matches the LV name).
	VolumeName string `json:"volumeName"`
	// SizeBytes is the size of the volume in bytes.
	SizeBytes int64 `json:"sizeBytes"`
	// PrimaryNode is the node currently serving the volume.
	PrimaryNode string `json:"primaryNode"`
	// SecondaryNode is the replication target. Set by the replication controller.
	SecondaryNode string `json:"secondaryNode,omitempty"`
	// VGName is the LVM volume group name on both nodes.
	VGName string `json:"vgName"`
	// LVMType is the backing LV type (linear, striped).
	LVMType string `json:"lvmType"`
	// DRBDMinor is the DRBD minor device number.
	DRBDMinor int `json:"drbdMinor"`
	// DRBDPort is the TCP port used for replication.
	DRBDPort int `json:"drbdPort"`
	// DRBDProtocol is the DRBD replication protocol (A, B, or C).
	DRBDProtocol string `json:"drbdProtocol"`
}

// DRBDConnectionState represents the DRBD connection state.
type DRBDConnectionState string

const (
	ConnectionStateConnected    DRBDConnectionState = "Connected"
	ConnectionStateConnecting   DRBDConnectionState = "Connecting"
	ConnectionStateStandAlone   DRBDConnectionState = "StandAlone"
	ConnectionStateUnknown      DRBDConnectionState = "Unknown"
	ConnectionStateDisconnected DRBDConnectionState = ""
)

// DRBDDiskState represents the DRBD disk state.
type DRBDDiskState string

const (
	DiskStateUpToDate     DRBDDiskState = "UpToDate"
	DiskStateInconsistent DRBDDiskState = "Inconsistent"
	DiskStateDiskless     DRBDDiskState = "Diskless"
	DiskStateUnknown      DRBDDiskState = ""
)

// DRBDVolumePhase represents the overall phase of the DRBD volume.
type DRBDVolumePhase string

const (
	// VolumePhasePending means the secondary node has not been assigned yet.
	VolumePhasePending DRBDVolumePhase = "Pending"
	// VolumePhaseSecondaryAssigned means the secondary node was selected.
	VolumePhaseSecondaryAssigned DRBDVolumePhase = "SecondaryAssigned"
	// VolumePhasePrimaryReady means the primary node has set up its DRBD resource.
	VolumePhasePrimaryReady DRBDVolumePhase = "PrimaryReady"
	// VolumePhaseSecondaryReady means the secondary node has set up its DRBD resource.
	VolumePhaseSecondaryReady DRBDVolumePhase = "SecondaryReady"
	// VolumePhaseEstablished means both sides are connected and UpToDate.
	VolumePhaseEstablished DRBDVolumePhase = "Established"
	// VolumePhaseDegraded means the replication link is broken.
	VolumePhaseDegraded DRBDVolumePhase = "Degraded"
	// VolumePhaseDeleting means the volume is being torn down.
	VolumePhaseDeleting DRBDVolumePhase = "Deleting"
)

// DRBDVolumeStatus defines the observed state of a DRBD-replicated volume.
type DRBDVolumeStatus struct {
	// Phase is the current lifecycle phase of the DRBD volume.
	Phase DRBDVolumePhase `json:"phase,omitempty"`
	// ConnectionState is the DRBD connection state between primary and secondary.
	ConnectionState DRBDConnectionState `json:"connectionState,omitempty"`
	// PrimaryDiskState is the disk state on the primary node.
	PrimaryDiskState DRBDDiskState `json:"primaryDiskState,omitempty"`
	// SecondaryDiskState is the disk state on the secondary node.
	SecondaryDiskState DRBDDiskState `json:"secondaryDiskState,omitempty"`
	// PrimaryReady indicates the primary node has completed DRBD setup.
	PrimaryReady bool `json:"primaryReady,omitempty"`
	// SecondaryReady indicates the secondary node has completed DRBD setup.
	SecondaryReady bool `json:"secondaryReady,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Primary",type=string,JSONPath=`.spec.primaryNode`
// +kubebuilder:printcolumn:name="Secondary",type=string,JSONPath=`.spec.secondaryNode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.status.connectionState`

// DRBDVolume represents a DRBD-replicated logical volume spanning two nodes.
type DRBDVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRBDVolumeSpec   `json:"spec,omitempty"`
	Status DRBDVolumeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRBDVolumeList contains a list of DRBDVolume resources.
type DRBDVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRBDVolume `json:"items"`
}
