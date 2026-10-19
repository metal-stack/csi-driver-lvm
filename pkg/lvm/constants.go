package lvm

const (
	// VolumeDriverTag marks logical volumes created by this driver.
	VolumeDriverTag = "lv.metal-stack.io/csi-lvm-driver"
	// SnapshotDriverTag marks snapshot logical volumes created by this driver.
	SnapshotDriverTag = "snapshot.metal-stack.io/csi-lvm-driver"

	// TagSnapshotName holds the CSI request name of a snapshot.
	TagSnapshotName = "csi-snapshot-name"
	// TagSourceVolume holds the volume ID a snapshot was taken from.
	TagSourceVolume = "csi-source-volume"
	// TagSourceSize holds the origin size in bytes at snapshot time.
	TagSourceSize = "csi-source-size"
	// TagRestoredFrom holds the snapshot ID a volume was restored from.
	TagRestoredFrom = "csi-restored-from"
	// TagRestoreState holds the progress of a restore copy.
	TagRestoreState = "csi-restore-state"

	// TagVolumeMode holds whether a volume, or the source of a snapshot, is a block or filesystem volume.
	TagVolumeMode = "csi-volume-mode"

	// VolumeModeBlock marks raw block volumes.
	VolumeModeBlock = "block"
	// VolumeModeFilesystem marks filesystem volumes.
	VolumeModeFilesystem = "filesystem"

	// RestoreStateInProgress marks a volume whose restore copy has not finished.
	RestoreStateInProgress = "in-progress"
	// RestoreStateDone marks a volume whose restore copy finished and was synced.
	RestoreStateDone = "done"

	tagKeyValueSeparator = "="
	tagListSeparator     = ","
	// tagSpecialChars are the non-alphanumeric characters LVM allows in tags.
	tagSpecialChars       = "_+.-/=!:&#"
	tagInvalidReplacement = '_'

	lvsCmd      = "lvs"
	lvcreateCmd = "lvcreate"
	lvchangeCmd = "lvchange"

	flagAddTag = "--addtag"
	flagDelTag = "--deltag"

	lvPathSeparator = "/"

	lvsSelectName   = "lv_name"
	lvsSelectOrigin = "origin"

	lvsReportTimeFormat = `report{time_format="%s"}`
	lvsReportFields     = "lv_name,vg_name,origin,origin_size,lv_size,lv_time,lv_tags,lv_snapshot_invalid,lv_attr"
	lvsSnapshotInvalid  = "1"

	insufficientFreeSpaceMessage = "Insufficient free space"
)
