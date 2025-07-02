/*
 *
 * Copyright © 2021-2023 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/csi-powerstore/v2/pkg/identifiers"
	"github.com/dell/csi-powerstore/v2/pkg/identifiers/fs"
	"github.com/dell/csm-sharednfs/nfs"
	"github.com/dell/gobrick"
	csictx "github.com/dell/gocsi/context"
	"github.com/dell/gofsutil"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	powerStoreMaxNodeNameLength = 64
	blockVolumePathMarker       = "/csi/volumeDevices/publish/"
	sysBlock                    = "/sys/block/"
	dev                         = "/dev/"
	defaultNodeNamePrefix       = "csi-node"
	defaultNodeChrootPath       = "/noderoot"

	// default opts values
	defaultTmpDir = "tmp"

	ephemeralStagingMountPath = "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/ephemeral/"

	commonNfsVolumeFolder = "common_folder"
)

// ISCSIConnector is wrapper of gobrcik.ISCSIConnector interface.
// It allows to connect iSCSI volumes to the node.
type ISCSIConnector interface {
	ConnectVolume(ctx context.Context, info gobrick.ISCSIVolumeInfo) (gobrick.Device, error)
	DisconnectVolumeByDeviceName(ctx context.Context, name string) error
	GetInitiatorName(ctx context.Context) ([]string, error)
}

// NVMEConnector is wrapper of gobrick.NVMEConnector interface.
// It allows to connect NVMe volumes to the node.
type NVMEConnector interface {
	ConnectVolume(ctx context.Context, info gobrick.NVMeVolumeInfo, useFC bool) (gobrick.Device, error)
	DisconnectVolumeByDeviceName(ctx context.Context, name string) error
	GetInitiatorName(ctx context.Context) ([]string, error)
}

// FcConnector is wrapper of gobrcik.FcConnector interface.
// It allows to connect FC volumes to the node.
type FcConnector interface {
	ConnectVolume(ctx context.Context, info gobrick.FCVolumeInfo) (gobrick.Device, error)
	DisconnectVolumeByDeviceName(ctx context.Context, name string) error
	GetInitiatorPorts(ctx context.Context) ([]string, error)
}

func getNodeOptions() Opts {
	var opts Opts
	ctx := context.Background()

	if path, ok := csictx.LookupEnv(ctx, identifiers.EnvNodeIDFilePath); ok {
		opts.NodeIDFilePath = path
	}

	if kubeConfigPath, ok := csictx.LookupEnv(ctx, identifiers.EnvKubeConfigPath); ok {
		opts.KubeConfigPath = kubeConfigPath
	}

	if prefix, ok := csictx.LookupEnv(ctx, identifiers.EnvNodeNamePrefix); ok {
		opts.NodeNamePrefix = prefix
	}

	if opts.NodeNamePrefix == "" {
		opts.NodeNamePrefix = defaultNodeNamePrefix
	}

	if kubeNodeName, ok := csictx.LookupEnv(ctx, identifiers.EnvKubeNodeName); ok {
		opts.KubeNodeName = kubeNodeName
	}

	if nodeChrootPath, ok := csictx.LookupEnv(ctx, identifiers.EnvNodeChrootPath); ok {
		opts.NodeChrootPath = nodeChrootPath
	}

	if opts.NodeChrootPath == "" {
		opts.NodeChrootPath = defaultNodeChrootPath
	}

	if maxVolumesPerNodeStr, ok := csictx.LookupEnv(ctx, identifiers.EnvMaxVolumesPerNode); ok {
		maxVolumesPerNode, err := strconv.ParseInt(maxVolumesPerNodeStr, 10, 64)
		if err != nil {
			log.Warn("error while parsing the value of maxPowerstoreVolumesPerNode, using default value 0")
			opts.MaxVolumesPerNode = 0
		} else {
			opts.MaxVolumesPerNode = maxVolumesPerNode
		}
	}

	if tmpDir, ok := csictx.LookupEnv(ctx, identifiers.EnvTmpDir); ok {
		opts.TmpDir = tmpDir
	}

	if opts.TmpDir == "" {
		opts.TmpDir = defaultTmpDir
	}

	if fcPortsFilterFilePath, ok := csictx.LookupEnv(ctx, identifiers.EnvFCPortsFilterFilePath); ok {
		opts.FCPortsFilterFilePath = fcPortsFilterFilePath
	}

	// pb parses an environment variable into a boolean value. If an error
	// is encountered, default is set to false, and error is logged
	pb := func(n string) bool {
		if v, ok := csictx.LookupEnv(ctx, n); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				log.WithField(n, v).Debug("invalid boolean value. defaulting to false")
				return false
			}
			return b
		}
		return false
	}

	opts.EnableCHAP = pb(identifiers.EnvEnableCHAP)

	if opts.EnableCHAP {
		opts.CHAPUsername = "admin"
		opts.CHAPPassword = identifiers.RandomString(12)
	}

	return opts
}

func formatWWPN(data string) (string, error) {
	var buffer bytes.Buffer
	for i, v := range data {
		_, err := buffer.WriteRune(v)
		if err != nil {
			return "", err
		}
		if i%2 != 0 && i < len(data)-1 {
			_, err := buffer.WriteString(":")
			if err != nil {
				return "", err
			}
		}
	}
	return buffer.String(), nil
}

// Get preferred outbound ip of this machine
func getOutboundIP(endpoint string, fs fs.Interface) (net.IP, error) {
	conn, err := fs.NetDial(endpoint)
	if err != nil {
		return nil, err
	}
	defer conn.Close() // #nosec G307

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP, nil
}

func getStagedDev(ctx context.Context, stagePath string, fs fs.Interface) (string, error) {
	mountInfo, found, err := getTargetMount(ctx, stagePath, fs)
	if err != nil {
		return "", status.Errorf(codes.Internal,
			"can't check mounts for path %s: %s", stagePath, err.Error())
	}
	if !found {
		return "", nil
	}
	sourceDev := mountInfo.Device
	// for bind mounts
	if sourceDev == "devtmpfs" || sourceDev == "udev" {
		sourceDev = mountInfo.Source
	}
	return sourceDev, nil
}

func getStagingPath(ctx context.Context, sp string, volID string) (string, string) {
	if nfs.IsNFSVolumeID(volID) {
		return nfs.ToArrayVolumeID(volID), sp
	}
	logFields := identifiers.GetLogFields(ctx)
	if sp == "" || volID == "" {
		return volID, sp
	}
	stagingPath := path.Join(sp, volID)
	log.WithFields(logFields).Infof("staging path is: %s", stagingPath)
	return volID, path.Join(sp, volID)
}

func getRemnantTargetMounts(ctx context.Context, target string, fs fs.Interface) ([]gofsutil.Info, bool, error) {
	logFields := identifiers.GetLogFields(ctx)
	var targetMounts []gofsutil.Info
	var found bool
	mounts, err := getMounts(ctx, fs)
	if err != nil {
		log.Error("could not reliably determine existing mount status")
		return targetMounts, false, status.Error(codes.Internal, "could not reliably determine existing mount status")
	}
	for _, mount := range mounts {
		if strings.Contains(mount.Path, target) {
			targetMounts = append(targetMounts, mount)
			log.WithFields(logFields).Infof("matching remnantTargetMount %s target %s", target, mount.Path)
			found = true
		}
	}
	return targetMounts, found, nil
}

func getTargetMount(ctx context.Context, target string, fs fs.Interface) (gofsutil.Info, bool, error) {
	logFields := identifiers.GetLogFields(ctx)
	var targetMount gofsutil.Info
	var found bool
	mounts, err := getMounts(ctx, fs)
	if err != nil {
		log.Error("could not reliably determine existing mount status")
		return targetMount, false, status.Error(codes.Internal,
			"could not reliably determine existing mount status")
	}
	for _, mount := range mounts {
		if mount.Path == target {
			targetMount = mount
			log.WithFields(logFields).Infof("matching targetMount %s target %s",
				target, mount.Path)
			found = true
			break
		}
	}
	return targetMount, found, nil
}

func getMounts(_ context.Context, fs fs.Interface) ([]gofsutil.Info, error) {
	data, err := consistentRead(procMountsPath, procMountsRetries, fs)
	if err != nil {
		return []gofsutil.Info{}, err
	}

	info, err := fs.ParseProcMounts(context.Background(), bytes.NewReader(data))
	if err != nil {
		return []gofsutil.Info{}, err
	}
	return info, nil
}

func consistentRead(filename string, retry int, fs fs.Interface) ([]byte, error) {
	oldContent, err := fs.ReadFile(filepath.Clean(filename))
	if err != nil {
		return nil, err
	}
	for i := 0; i < retry; i++ {
		newContent, err := fs.ReadFile(filepath.Clean(filename))
		if err != nil {
			return nil, err
		}
		if bytes.Compare(oldContent, newContent) == 0 {
			log.Infof("successfully read mount file snapshot retry count: %d", i)
			return newContent, nil
		}
		// Files are different, continue reading
		oldContent = newContent
	}
	return nil, fmt.Errorf("could not get consistent content of %s after %d attempts", filename, retry)
}

func createMapping(volID, deviceName, tmpDir string, fs fs.Interface) error {
	return fs.WriteFile(path.Join(tmpDir, volID), []byte(deviceName), 0o640)
}

func getMapping(volID, tmpDir string, fs fs.Interface) (string, error) {
	data, err := fs.ReadFile(path.Join(tmpDir, volID))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("no device name in mapping")
	}
	return string(data), nil
}

func deleteMapping(volID, tmpDir string, fs fs.Interface) error {
	err := fs.Remove(path.Join(tmpDir, volID))
	if fs.IsNotExist(err) {
		return nil
	}
	return err
}

func isBlock(vc *csi.VolumeCapability) bool {
	_, isBlock := vc.GetAccessType().(*csi.VolumeCapability_Block)
	return isBlock
}

func isAlreadyPublished(ctx context.Context, targetPath, rwMode string, fs fs.Interface) (bool, error) {
	mount, found, err := getTargetMount(ctx, targetPath, fs)
	if err != nil {
		return false, status.Errorf(codes.Internal,
			"can't check mounts for path %s: %s", targetPath, err.Error())
	}
	if !found {
		return false, nil
	}
	if !contains(mount.Opts, rwMode) {
		return false, status.Errorf(codes.FailedPrecondition,
			"volume already mounted but with different capabilities: %s",
			mount.Opts)
	}
	return true, nil
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func getRWModeString(isRO bool) string {
	if isRO {
		return "ro"
	}
	return "rw"
}

func format(_ context.Context, source, fsType string, fs fs.Interface, opts ...string) error {
	f := log.Fields{
		"source":  source,
		"fsType":  fsType,
		"options": opts,
	}

	// Use 'ext4' as the default
	if fsType == "" {
		fsType = "ext4"
	}

	mkfsCmd := fmt.Sprintf("mkfs.%s", fsType)
	mkfsArgs := []string{"-E", "nodiscard", "-F", source}

	if fsType == "xfs" {
		mkfsArgs = []string{"-K", source}
	}
	mkfsArgs = append(mkfsArgs, opts...)

	log.WithFields(f).Infof("formatting with command: %s %v", mkfsCmd, mkfsArgs)
	out, err := fs.ExecCommand(mkfsCmd, mkfsArgs...)
	if err != nil {
		log.WithFields(f).WithError(err).Errorf("formatting disk failed, output: %q", string(out))
		return errors.New(string(out))
	}

	return nil
}
