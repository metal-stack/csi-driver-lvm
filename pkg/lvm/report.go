package lvm

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LogicalVolume is a parsed row of an lvs report.
type LogicalVolume struct {
	Name            string
	VG              string
	Origin          string
	Size            int64
	OriginSize      int64
	CreationTime    time.Time
	Tags            map[string]string
	SnapshotInvalid bool
	Attr            string
}

type lvReport struct {
	Report []struct {
		LV []lvReportRow `json:"lv"`
	} `json:"report"`
}

type lvReportRow struct {
	LVName            string `json:"lv_name"`
	VGName            string `json:"vg_name"`
	Origin            string `json:"origin"`
	OriginSize        string `json:"origin_size"`
	LVSize            string `json:"lv_size"`
	LVTime            string `json:"lv_time"`
	LVTags            string `json:"lv_tags"`
	LVSnapshotInvalid string `json:"lv_snapshot_invalid"`
	LVAttr            string `json:"lv_attr"`
}

// IsSnapshot reports whether the logical volume is a thick snapshot of another LV.
func (lv LogicalVolume) IsSnapshot() bool {
	return lv.Origin != ""
}

// HasTag reports whether the logical volume carries the given tag key.
func (lv LogicalVolume) HasTag(key string) bool {
	_, ok := lv.Tags[key]
	return ok
}

// Tag builds a key=value LVM tag with the value sanitized to the LVM tag charset.
func Tag(key, value string) string {
	return key + tagKeyValueSeparator + SanitizeTagValue(value)
}

// SanitizeTagValue replaces characters LVM does not allow in tags with an underscore.
func SanitizeTagValue(value string) string {
	return strings.Map(func(r rune) rune {
		if isTagRune(r) {
			return r
		}
		return tagInvalidReplacement
	}, value)
}

func isTagRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case strings.ContainsRune(tagSpecialChars, r):
		return true
	default:
		return false
	}
}

// GetLV returns the named logical volume, or nil if it does not exist.
func GetLV(log *slog.Logger, vg, name string) (*LogicalVolume, error) {
	lvs, err := listLVs(log, vg, selectEquals(lvsSelectName, name))
	if err != nil {
		return nil, err
	}
	if len(lvs) == 0 {
		return nil, nil
	}
	return &lvs[0], nil
}

// ListLVs returns all logical volumes of the volume group.
func ListLVs(log *slog.Logger, vg string) ([]LogicalVolume, error) {
	return listLVs(log, vg, "")
}

func listLVs(log *slog.Logger, vg, selection string) ([]LogicalVolume, error) {
	args := []string{
		vg,
		"--reportformat", "json",
		"--binary",
		"--units", "b",
		"--nosuffix",
		"--config", lvsReportTimeFormat,
		"-o", lvsReportFields,
	}
	if selection != "" {
		args = append(args, "-S", selection)
	}

	log.Debug(lvsCmd, "args", args)

	// stdout only, since lvs prints warnings to stderr that would break the JSON.
	out, err := exec.Command(lvsCmd, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("unable to list logical volumes of vg %q: %w (%s)", vg, err, stderrOf(err))
	}

	return parseLVReport(out)
}

func parseLVReport(out []byte) ([]LogicalVolume, error) {
	report := lvReport{}
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("failed to parse lvs output: %w", err)
	}

	var rows []lvReportRow
	for _, r := range report.Report {
		rows = append(rows, r.LV...)
	}

	lvs := make([]LogicalVolume, 0, len(rows))
	for _, row := range rows {
		lv, err := row.toLogicalVolume()
		if err != nil {
			return nil, err
		}
		lvs = append(lvs, lv)
	}

	return lvs, nil
}

// selectEquals builds an lvs selection that matches a field against a quoted value.
func selectEquals(field, value string) string {
	return fmt.Sprintf("%s=%q", field, value)
}

// qualifiedName returns the vg/lv name LVM commands expect.
func qualifiedName(vg, name string) string {
	return vg + lvPathSeparator + name
}

func (row lvReportRow) toLogicalVolume() (LogicalVolume, error) {
	size, err := parseReportInt(row.LVSize)
	if err != nil {
		return LogicalVolume{}, fmt.Errorf("invalid lv_size of %s: %w", row.LVName, err)
	}
	originSize, err := parseReportInt(row.OriginSize)
	if err != nil {
		return LogicalVolume{}, fmt.Errorf("invalid origin_size of %s: %w", row.LVName, err)
	}
	created, err := parseReportInt(row.LVTime)
	if err != nil {
		return LogicalVolume{}, fmt.Errorf("invalid lv_time of %s: %w", row.LVName, err)
	}

	return LogicalVolume{
		Name:            row.LVName,
		VG:              row.VGName,
		Origin:          row.Origin,
		Size:            size,
		OriginSize:      originSize,
		CreationTime:    time.Unix(created, 0),
		Tags:            parseTags(row.LVTags),
		SnapshotInvalid: row.LVSnapshotInvalid == lvsSnapshotInvalid,
		Attr:            row.LVAttr,
	}, nil
}

func parseReportInt(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func parseTags(raw string) map[string]string {
	tags := map[string]string{}
	for tag := range strings.SplitSeq(raw, tagListSeparator) {
		if tag == "" {
			continue
		}
		key, value, _ := strings.Cut(tag, tagKeyValueSeparator)
		tags[key] = value
	}
	return tags
}

func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}
