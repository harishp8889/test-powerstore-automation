/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *      http://www.apache.org/licenses/LICENSE-2.0
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/dell/csi-powerstore/v2/pkg/array"
	"github.com/dell/csm-hbnfs/nfs"
	csiext "github.com/dell/dell-csi-extensions/replication"
	"github.com/dell/gopowerstore"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateRemoteVolume creates replica of volume in remote cluster
func (s *Service) CreateRemoteVolume(ctx context.Context,
	req *csiext.CreateRemoteVolumeRequest,
) (*csiext.CreateRemoteVolumeResponse, error) {
	volID := req.GetVolumeHandle()
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	params := req.GetParameters()

	volumeHandle, err := array.ParseVolumeID(ctx, volID, s.DefaultArray(), nil)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	id := volumeHandle.LocalUUID
	arrayID := volumeHandle.LocalArrayGlobalID
	protocol := volumeHandle.Protocol

	volPrefix := ""
	if accessMode, ok := params[nfs.CsiNfsParameter]; ok && accessMode != "" {
		// host-based nfs volumes should have the "csi-nfs" parameter
		// and an "nfs-" prefix in the volume ID that we need to remove
		// for gopowerstore queries to succeed.
		// Remove the prefix here and restore it when building the volume ID
		// for the function response.
		volPrefix = array.GetVolumeUUIDPrefix(id)
		id = strings.TrimPrefix(id, volPrefix)
	}

	arr, ok := s.Arrays()[arrayID]
	if !ok {
		log.Info("ip is nil")
		return nil, status.Error(codes.InvalidArgument, "failed to find array with given IP")
	}

	vgs, err := arr.GetClient().GetVolumeGroupsByVolumeID(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(vgs.VolumeGroup) == 0 {
		return nil, status.Error(codes.Unimplemented, "replication of volumes that aren't assigned to group is not implemented yet")
	}
	vg := vgs.VolumeGroup[0]

	rs, err := arr.Client.GetReplicationSessionByLocalResourceID(ctx, vg.ID)
	if err != nil {
		return nil, err
	}

	var remoteVolumeID string
	for _, sp := range rs.StorageElementPairs {
		if sp.LocalStorageElementID == id {
			remoteVolumeID = sp.RemoteStorageElementID
		}
	}

	if remoteVolumeID == "" {
		return nil, status.Errorf(codes.Internal, "couldn't find volume id %s in storage element pairs of replication session", id)
	}

	vol, err := arr.Client.GetVolume(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "can't query volume: %s", err.Error())
	}
	localSystem, err := arr.Client.GetCluster(ctx)
	if err != nil {
		return nil, err
	}
	remoteSystem, err := arr.Client.GetRemoteSystem(ctx, rs.RemoteSystemID)
	if err != nil {
		return nil, err
	}

	remoteParams := map[string]string{
		"remoteSystem":                                   localSystem.Name,
		s.replicationContextPrefix + "arrayID":           remoteSystem.SerialNumber,
		s.replicationContextPrefix + "managementAddress": remoteSystem.ManagementAddress,
	}
	remoteVolume := getRemoteCSIVolume(
		volPrefix+remoteVolumeID+"/"+remoteParams[s.replicationContextPrefix+"arrayID"]+"/"+protocol,
		vol.Size,
	)
	remoteVolume.VolumeContext = remoteParams
	return &csiext.CreateRemoteVolumeResponse{
		RemoteVolume: remoteVolume,
	}, nil
}

// CreateStorageProtectionGroup creates storage protection group
func (s *Service) CreateStorageProtectionGroup(ctx context.Context,
	req *csiext.CreateStorageProtectionGroupRequest,
) (*csiext.CreateStorageProtectionGroupResponse, error) {
	volID := req.GetVolumeHandle()
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	params := req.GetParameters()

	volumeHandle, err := array.ParseVolumeID(ctx, volID, s.DefaultArray(), nil)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	id := volumeHandle.LocalUUID
	arrayID := volumeHandle.LocalArrayGlobalID
	protocol := volumeHandle.Protocol

	if accessMode, ok := params[nfs.CsiNfsParameter]; ok && accessMode != "" {
		// host-based nfs volumes should have the "csi-nfs" parameter
		// and a "nfs-" prefix in the volume ID that we need to remove
		// for gopowerstore queries to succeed
		volPrefix := array.GetVolumeUUIDPrefix(id)
		id = strings.TrimPrefix(id, volPrefix)
	}

	arr, ok := s.Arrays()[arrayID]
	if !ok {
		log.Info("id is nil")
		return nil, status.Error(codes.InvalidArgument, "failed to find array with given ID")
	}

	if protocol == "nfs" {
		return nil, status.Error(codes.InvalidArgument, "replication is not supported for NFS volumes")
	}

	vgs, err := arr.GetClient().GetVolumeGroupsByVolumeID(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(vgs.VolumeGroup) == 0 {
		return nil, status.Error(codes.Unimplemented, "replication of volumes that aren't assigned to group is not implemented yet")
	}
	vg := vgs.VolumeGroup[0]

	rs, err := arr.Client.GetReplicationSessionByLocalResourceID(ctx, vg.ID)
	if err != nil {
		return nil, err
	}

	localSystem, err := arr.Client.GetCluster(ctx)
	if err != nil {
		return nil, err
	}

	remoteSystem, err := arr.Client.GetRemoteSystem(ctx, rs.RemoteSystemID)
	if err != nil {
		return nil, err
	}
	localParams := map[string]string{
		s.replicationContextPrefix + "systemName":              localSystem.Name,
		s.replicationContextPrefix + "managementAddress":       localSystem.ManagementAddress,
		s.replicationContextPrefix + "remoteSystemName":        remoteSystem.Name,
		s.replicationContextPrefix + "remoteManagementAddress": remoteSystem.ManagementAddress,
		s.replicationContextPrefix + "globalID":                arrayID,
		s.replicationContextPrefix + "remoteGlobalID":          remoteSystem.SerialNumber,
		s.replicationContextPrefix + "VolumeGroupName":         vg.Name,
	}
	remoteParams := map[string]string{
		s.replicationContextPrefix + "systemName":              remoteSystem.Name,
		s.replicationContextPrefix + "managementAddress":       remoteSystem.ManagementAddress,
		s.replicationContextPrefix + "remoteSystemName":        localSystem.Name,
		s.replicationContextPrefix + "remoteManagementAddress": localSystem.ManagementAddress,
		s.replicationContextPrefix + "globalID":                remoteSystem.SerialNumber,
		s.replicationContextPrefix + "VolumeGroupName":         vg.Name,
	}

	return &csiext.CreateStorageProtectionGroupResponse{
		LocalProtectionGroupId:          rs.LocalResourceID,
		RemoteProtectionGroupId:         rs.RemoteResourceID,
		LocalProtectionGroupAttributes:  localParams,
		RemoteProtectionGroupAttributes: remoteParams,
	}, nil
}

// EnsureProtectionPolicyExists  ensures protection policy exists
func EnsureProtectionPolicyExists(ctx context.Context, arr *array.PowerStoreArray,
	vgName string, remoteSystemName string, rpoEnum gopowerstore.RPOEnum,
) (string, error) {
	// Get id of specified remote system
	rs, err := arr.Client.GetRemoteSystemByName(ctx, remoteSystemName)
	if err != nil {
		return "", status.Errorf(codes.Internal, "can't query remote system by name: %s", err.Error())
	}

	ppName := "pp-" + vgName

	// Check that protection policy already exists
	pp, err := arr.Client.GetProtectionPolicyByName(ctx, ppName)
	if err == nil {
		return pp.ID, nil
	}

	// ensure that replicationRule exists
	rrID, err := EnsureReplicationRuleExists(ctx, arr, vgName, rs.ID, rpoEnum)
	if err != nil {
		return "", status.Errorf(codes.Internal, "can't ensure that replication rule exists")
	}

	newPp, err := arr.Client.CreateProtectionPolicy(ctx, &gopowerstore.ProtectionPolicyCreate{
		Name:               ppName,
		ReplicationRuleIDs: []string{rrID},
	})
	if err != nil {
		return "", status.Errorf(codes.Internal, "can't create protection policy: %s", err.Error())
	}

	return newPp.ID, nil
}

// EnsureReplicationRuleExists ensures replication rule exists
func EnsureReplicationRuleExists(ctx context.Context, arr *array.PowerStoreArray,
	vgName string, remoteSystemID string, rpoEnum gopowerstore.RPOEnum,
) (string, error) {
	rrName := "rr-" + vgName
	rr, err := arr.Client.GetReplicationRuleByName(ctx, rrName)
	if err != nil {
		// Create new rule
		newRr, err := arr.Client.CreateReplicationRule(ctx, &gopowerstore.ReplicationRuleCreate{
			Name:           rrName,
			Rpo:            rpoEnum,
			RemoteSystemID: remoteSystemID,
		})
		if err != nil {
			return "", status.Errorf(codes.Internal, "can't create replication rule: %s", err.Error())
		}
		return newRr.ID, nil
	}
	return rr.ID, nil
}

// GetReplicationCapabilities is a getter for replication capabilities
func (s *Service) GetReplicationCapabilities(_ context.Context, _ *csiext.GetReplicationCapabilityRequest) (*csiext.GetReplicationCapabilityResponse, error) {
	rep := new(csiext.GetReplicationCapabilityResponse)
	rep.Capabilities = []*csiext.ReplicationCapability{
		{
			Type: &csiext.ReplicationCapability_Rpc{
				Rpc: &csiext.ReplicationCapability_RPC{
					Type: csiext.ReplicationCapability_RPC_CREATE_REMOTE_VOLUME,
				},
			},
		},
		{
			Type: &csiext.ReplicationCapability_Rpc{
				Rpc: &csiext.ReplicationCapability_RPC{
					Type: csiext.ReplicationCapability_RPC_CREATE_PROTECTION_GROUP,
				},
			},
		},
		{
			Type: &csiext.ReplicationCapability_Rpc{
				Rpc: &csiext.ReplicationCapability_RPC{
					Type: csiext.ReplicationCapability_RPC_DELETE_PROTECTION_GROUP,
				},
			},
		},
		{
			Type: &csiext.ReplicationCapability_Rpc{
				Rpc: &csiext.ReplicationCapability_RPC{
					Type: csiext.ReplicationCapability_RPC_REPLICATION_ACTION_EXECUTION,
				},
			},
		},
		{
			Type: &csiext.ReplicationCapability_Rpc{
				Rpc: &csiext.ReplicationCapability_RPC{
					Type: csiext.ReplicationCapability_RPC_MONITOR_PROTECTION_GROUP,
				},
			},
		},
	}
	rep.Actions = []*csiext.SupportedActions{
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_FAILOVER_REMOTE,
			},
		},
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_UNPLANNED_FAILOVER_LOCAL,
			},
		},
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_REPROTECT_LOCAL,
			},
		},
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_SUSPEND,
			},
		},
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_RESUME,
			},
		},
		{
			Actions: &csiext.SupportedActions_Type{
				Type: csiext.ActionTypes_SYNC,
			},
		},
	}
	return rep, nil
}

// ExecuteAction is a method to execute an action request
func (s *Service) ExecuteAction(ctx context.Context,
	req *csiext.ExecuteActionRequest,
) (*csiext.ExecuteActionResponse, error) {
	var reqID string
	localParams := req.GetProtectionGroupAttributes()
	protectionGroupID := req.GetProtectionGroupId()
	action := req.GetAction().GetActionTypes().String()
	globalID, ok := localParams[s.replicationContextPrefix+"globalID"]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "missing globalID in protection group attributes")
	}
	arr, ok := s.Arrays()[globalID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "can't find array with global id %s", globalID)
	}
	pstoreClient := arr.GetClient()

	// log all parameters used in ExecuteAction call
	fields := map[string]interface{}{
		"RequestID":             reqID,
		"GlobalID":              localParams[s.replicationContextPrefix+"globalID"],
		"ProtectedStorageGroup": protectionGroupID,
		"Action":                action,
	}
	log.WithFields(fields).Info("Executing ExecuteAction with following fields")
	rs, err := pstoreClient.GetReplicationSessionByLocalResourceID(ctx, protectionGroupID)
	if err != nil {
		return nil, err
	}
	client := pstoreClient
	var execAction gopowerstore.ActionType
	var params *gopowerstore.FailoverParams
	switch action {
	case csiext.ActionTypes_FAILOVER_REMOTE.String():
		execAction = gopowerstore.RsActionFailover
		params = &gopowerstore.FailoverParams{IsPlanned: true, Reverse: false}
	case csiext.ActionTypes_UNPLANNED_FAILOVER_LOCAL.String():
		execAction = gopowerstore.RsActionFailover
		params = &gopowerstore.FailoverParams{IsPlanned: false, Reverse: false}
	case csiext.ActionTypes_SUSPEND.String():
		execAction = gopowerstore.RsActionPause
	case csiext.ActionTypes_RESUME.String():
		execAction = gopowerstore.RsActionResume
	case csiext.ActionTypes_SYNC.String():
		execAction = gopowerstore.RsActionSync
	case csiext.ActionTypes_REPROTECT_LOCAL.String():
		execAction = gopowerstore.RsActionReprotect
	default:
		return nil, status.Errorf(codes.Unknown, "The requested action does not match with supported actions")
	}
	resErr := ExecuteAction(&rs, client, execAction, params)
	if resErr != nil {
		return nil, resErr
	}

	statusResp, err := s.GetStorageProtectionGroupStatus(ctx, &csiext.GetStorageProtectionGroupStatusRequest{
		ProtectionGroupId:         protectionGroupID,
		ProtectionGroupAttributes: localParams,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "can't get storage protection group status: %s", err.Error())
	}

	resp := &csiext.ExecuteActionResponse{
		Success: true,
		ActionTypes: &csiext.ExecuteActionResponse_Action{
			Action: req.GetAction(),
		},
		Status: statusResp.Status,
	}
	return resp, nil
}

// ExecuteAction validates current state of replication & executes provided action on RS
func ExecuteAction(session *gopowerstore.ReplicationSession, pstoreClient gopowerstore.Client, action gopowerstore.ActionType, failoverParams *gopowerstore.FailoverParams) error {
	inDesiredState, actionRequired, err := validateRSState(session, action)
	if err != nil {
		return err
	}

	if !inDesiredState {
		if !actionRequired {
			return status.Errorf(codes.Aborted, "Execute action: RS (%s) is still executing previous action", session.ID)
		}

		_, err := pstoreClient.ExecuteActionOnReplicationSession(context.Background(), session.ID, action,
			failoverParams)
		if err != nil {
			if apiError, ok := err.(gopowerstore.APIError); ok && apiError.UnableToFailoverFromDestination() {
				log.Error(fmt.Sprintf("Fail over: Failed to modify RS (%s) - Error (%s)", session.ID, err.Error()))
				return status.Errorf(codes.Internal, "Execute action: Failed to modify RS (%s) - Error (%s)", session.ID, err.Error())
			}
		}
		log.Debugf("Action (%s) successful on RS(%s)", string(action), session.ID)
	}
	return nil
}

// validateRSState checks if the given action is permissible on the protected storage group based on its current state
func validateRSState(session *gopowerstore.ReplicationSession, action gopowerstore.ActionType) (inDesiredState bool, actionRequired bool, resErr error) {
	state := session.State
	log.Infof("replication session is in %s", state)
	switch action {
	case gopowerstore.RsActionResume:
		if state == "OK" {
			log.Infof("RS (%s) is already in desired state: (%s)", session.ID, state)
			return true, false, nil
		}
	case gopowerstore.RsActionReprotect:
		if state == "OK" {
			log.Infof("RS (%s) is already in desired state: (%s)", session.ID, state)
			return true, false, nil
		}
	case gopowerstore.RsActionPause:
		if state == "Paused" || state == "Paused_For_Migration" || state == "Paused_For_NDU" {
			log.Infof("RS (%s) is already in desired state: (%s)", session.ID, state)
			return true, false, nil
		}
	case gopowerstore.RsActionFailover:
		if state == "Failing_Over" {
			return false, false, nil
		}
		if state == "Failed_Over" {
			log.Infof("RS (%s) is already in desired state: (%s)", session.ID, state)
			return true, false, nil
		}
	}
	return false, true, nil
}

// DeleteStorageProtectionGroup deletes storage protection group
func (s *Service) DeleteStorageProtectionGroup(ctx context.Context,
	req *csiext.DeleteStorageProtectionGroupRequest,
) (*csiext.DeleteStorageProtectionGroupResponse, error) {
	localParams := req.GetProtectionGroupAttributes()
	groupID := req.GetProtectionGroupId()
	globalID, ok := localParams[s.replicationContextPrefix+"globalID"]

	if !ok {
		return nil, status.Error(codes.InvalidArgument, "missing globalID in protection group attributes")
	}

	arr, ok := s.Arrays()[globalID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "can't find array with global id %s", globalID)
	}
	fields := map[string]interface{}{
		"GlobalID":              globalID,
		"ProtectedStorageGroup": groupID,
	}

	log.WithFields(fields).Info("Deleting storage protection group")

	vg, err := arr.GetClient().GetVolumeGroup(ctx, groupID)
	if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
		return nil, status.Errorf(codes.Internal, "Error: Unable to get Volume Group")
	}
	if vg.ID != "" {
		if vg.ProtectionPolicyID != "" {
			_, err := arr.GetClient().ModifyVolumeGroup(ctx, &gopowerstore.VolumeGroupModify{
				ProtectionPolicyID: "",
			}, groupID)
			if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
				return nil, status.Errorf(codes.Internal, "Error: Unable to un-assign PP from Volume Group")
			}
		}
		_, err = arr.Client.DeleteVolumeGroup(ctx, groupID)
		if apiError, ok := err.(gopowerstore.APIError); ok && !apiError.NotFound() {
			return nil, status.Errorf(codes.Internal, "Error: %s: Unable to delete Volume Group", apiError.Error())
		}
	}

	log.WithFields(fields).Info("Deleting protection policy")

	vgName, ok := localParams[s.replicationContextPrefix+"VolumeGroupName"]
	if !ok {
		return nil, status.Errorf(codes.Internal, "Error: Unable to get volume group name")
	}
	pp, err := arr.GetClient().GetProtectionPolicyByName(ctx, "pp-"+vgName)
	if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
		return nil, status.Errorf(codes.Internal, "Error: Unable to get the PP")
	}
	if pp.ID != "" && len(pp.Volumes) == 0 && len(pp.VolumeGroups) == 0 {
		_, err := arr.Client.DeleteProtectionPolicy(ctx, pp.ID)
		if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
			return nil, status.Errorf(codes.Internal, "Error: Unable to delete PP")
		}
	}

	log.WithFields(fields).Info("Deleting replication rule")

	rr, err := arr.GetClient().GetReplicationRuleByName(ctx, "rr-"+vgName)
	if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
		return nil, status.Errorf(codes.Internal, "Error: RR not found")
	}
	if rr.ID != "" && len(rr.ProtectionPolicies) == 0 {
		_, err = arr.GetClient().DeleteReplicationRule(ctx, rr.ID)
		if apiErr, ok := err.(gopowerstore.APIError); ok && !apiErr.NotFound() {
			return nil, status.Errorf(codes.Internal, "Error: Unable to delete replication rule")
		}
	}

	return &csiext.DeleteStorageProtectionGroupResponse{}, nil
}

// DeleteLocalVolume deletes a volume on the local storage array upon request from a remote replication controller.
func (s *Service) DeleteLocalVolume(ctx context.Context,
	req *csiext.DeleteLocalVolumeRequest,
) (*csiext.DeleteLocalVolumeResponse, error) {
	log.Info("Deleting local volume " + req.VolumeHandle + " per request from remote replication controller")

	// req.VolumeHandle is of format <volumeid>/<array ID>/<protocol>. We only need the IDs.
	splitHandle := strings.Split(req.VolumeHandle, `/`)
	if len(splitHandle) != 3 {
		return nil, status.Errorf(codes.InvalidArgument, "can't delete volume of improper handle format")
	}
	volumeID := splitHandle[0]
	globalID := splitHandle[1]

	arr, ok := s.Arrays()[globalID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "can't find array with global ID %s", globalID)
	}

	vol, err := arr.GetClient().GetVolume(ctx, volumeID)
	if err != nil {
		if apiError, ok := err.(gopowerstore.APIError); ok {
			if apiError.NotFound() {
				// volume doesn't exist, return success
				log.Info("Volume does not exist. It may have already been deleted.")
				return &csiext.DeleteLocalVolumeResponse{}, nil
			}
		}
		// any other error means the volume to be deleted couldn't be retrieved, return error
		return nil, status.Errorf(codes.Internal, "Error: Unable to get volume for deletion")
	}

	vgs, err := arr.GetClient().GetVolumeGroupsByVolumeID(ctx, volumeID)
	if err != nil {
		if apiError, ok := err.(gopowerstore.APIError); !ok || !apiError.NotFound() {
			return nil, err
		}
	}

	// Do not proceed to DeleteVolume if there is a volume group or protection policy.
	// DeleteVolume would remove those, and source-side deletion is the responsible party for that operation.
	if len(vgs.VolumeGroup) != 0 {
		log.Info("Cannot delete local volume " + volumeID + ", volume is part of a Volume Group and needs to be removed first.")
		return nil, status.Errorf(codes.Internal, "Error: Unable to delete volume")
	} else if vol.ProtectionPolicyID != "" {
		log.Info("Cannot delete local volume " + volumeID + ", volume is under a protection policy that must be removed first.")
		return nil, status.Errorf(codes.Internal, "Error: Unable to delete volume")
	}

	_, err = arr.GetClient().DeleteVolume(ctx, nil, volumeID)
	if err != nil {
		if apiErr, ok := err.(gopowerstore.APIError); !ok || !apiErr.NotFound() {
			log.Info("Cannot delete local volume " + volumeID + ", deletion returned a non-404 error code.")
			return nil, status.Errorf(codes.Internal, "Error: Unable to delete volume")
		}
	}

	log.Info("Local volume deleted successfully.")
	return &csiext.DeleteLocalVolumeResponse{}, nil
}

// GetStorageProtectionGroupStatus gets storage protection group status
func (s *Service) GetStorageProtectionGroupStatus(ctx context.Context,
	req *csiext.GetStorageProtectionGroupStatusRequest,
) (*csiext.GetStorageProtectionGroupStatusResponse, error) {
	localParams := req.GetProtectionGroupAttributes()
	groupID := req.GetProtectionGroupId()

	globalID, ok := localParams[s.replicationContextPrefix+"globalID"]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "missing globalID in protection group attributes")
	}

	arr, ok := s.Arrays()[globalID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "can't find array with global id %s", globalID)
	}
	fields := map[string]interface{}{
		"GlobalID":              globalID,
		"ProtectedStorageGroup": groupID,
	}
	log.WithFields(fields).Info("Checking replication session status")

	rs, err := arr.GetClient().GetReplicationSessionByLocalResourceID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	var state csiext.StorageProtectionGroupStatus_State
	switch rs.State {
	case gopowerstore.RsStateOk:
		state = csiext.StorageProtectionGroupStatus_SYNCHRONIZED
		break
	case gopowerstore.RsStateFailedOver:
		state = csiext.StorageProtectionGroupStatus_FAILEDOVER
		break
	case gopowerstore.RsStatePaused, gopowerstore.RsStatePausedForMigration, gopowerstore.RsStatePausedForNdu, gopowerstore.RsStateSystemPaused:
		state = csiext.StorageProtectionGroupStatus_SUSPENDED
		break
	case gopowerstore.RsStateFailingOver, gopowerstore.RsStateFailingOverForDR, gopowerstore.RsStateResuming,
		gopowerstore.RsStateReprotecting, gopowerstore.RsStatePartialCutoverForMigration, gopowerstore.RsStateSynchronizing,
		gopowerstore.RsStateInitializing:
		state = csiext.StorageProtectionGroupStatus_SYNC_IN_PROGRESS
		break
	case gopowerstore.RsStateError:
		state = csiext.StorageProtectionGroupStatus_INVALID
		break
	default:
		log.Infof("The status (%s) does not match with known protection group states", rs.State)
		state = csiext.StorageProtectionGroupStatus_UNKNOWN
		break
	}
	log.Infof("The current state for replication session (%s) for group (%s) is (%s).", rs.ID, groupID, state.String())
	resp := &csiext.GetStorageProtectionGroupStatusResponse{
		Status: &csiext.StorageProtectionGroupStatus{
			State:    state,
			IsSource: rs.Role != "Destination",
		},
	}
	return resp, err
}

// WithRP appends Replication Prefix to provided string
func (s *Service) WithRP(key string) string {
	replicationPrefix := s.replicationPrefix
	if replicationPrefix == "" {
		replicationPrefix = ReplicationPrefix
	}

	return replicationPrefix + "/" + key
}

func getRemoteCSIVolume(volumeID string, size int64) *csiext.Volume {
	volume := &csiext.Volume{
		CapacityBytes: size,
		VolumeId:      volumeID,
		VolumeContext: nil, // TODO: add values to volume context if needed
	}
	return volume
}
