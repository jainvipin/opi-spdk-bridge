// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2023 Intel Corporation

// Package kvm automates plugging of SPDK devices to a QEMU instance
package kvm

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opiproject/gospdk/spdk"
	pc "github.com/opiproject/opi-api/common/v1/gen/go"
	pb "github.com/opiproject/opi-api/storage/v1alpha1/gen/go"
	"github.com/opiproject/opi-spdk-bridge/pkg/frontend"
	"github.com/opiproject/opi-spdk-bridge/pkg/server"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var (
	testNvmeControllerID   = "nvme-43"
	testNvmeControllerName = server.ResourceIDToVolumeName("nvme-43")
	testSubsystemID        = "subsystem0"
	testSubsystemName      = server.ResourceIDToVolumeName("subsystem0")
	testSubsystem          = pb.NvmeSubsystem{
		Name: testSubsystemName,
		Spec: &pb.NvmeSubsystemSpec{
			Nqn: "nqn.2022-09.io.spdk:opi2",
		},
	}
	testCreateNvmeControllerRequest = &pb.CreateNvmeControllerRequest{NvmeControllerId: testNvmeControllerID, NvmeController: &pb.NvmeController{
		Spec: &pb.NvmeControllerSpec{
			SubsystemId:      &pc.ObjectKey{Value: testSubsystem.Name},
			PcieId:           &pb.PciEndpoint{PhysicalFunction: 43, VirtualFunction: 0},
			NvmeControllerId: 43,
		},
		Status: &pb.NvmeControllerStatus{
			Active: true,
		},
	}}
	testDeleteNvmeControllerRequest = &pb.DeleteNvmeControllerRequest{Name: testNvmeControllerName}
)

func TestNewVfiouserSubsystemListener(t *testing.T) {
	tests := map[string]struct {
		ctrlrDir  string
		wantPanic bool
	}{
		"valid controller dir": {
			ctrlrDir:  ".",
			wantPanic: false,
		},
		"empty string for controller dir": {
			ctrlrDir:  "",
			wantPanic: true,
		},
		"non existing path": {
			ctrlrDir:  "this/is/some/non/existing/path",
			wantPanic: true,
		},
		"ctrlrDir points to non-directory": {
			ctrlrDir:  "/dev/null",
			wantPanic: true,
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			defer func() {
				r := recover()
				if (r != nil) != tt.wantPanic {
					t.Errorf("NewVfiouserSubsystemListener() recover = %v, wantPanic = %v", r, tt.wantPanic)
				}
			}()

			gotSubsysListener := NewVfiouserSubsystemListener(tt.ctrlrDir)
			wantSubsysListener := &vfiouserSubsystemListener{
				ctrlrDir: tt.ctrlrDir,
			}

			if !reflect.DeepEqual(gotSubsysListener, wantSubsysListener) {
				t.Errorf("Received subsystem listern %v not equal to expected one %v", gotSubsysListener, wantSubsysListener)
			}
		})
	}
}

func TestNewVfiouserSubsystemListenerParams(t *testing.T) {
	tmpDir := os.TempDir()
	wantParams := spdk.NvmfSubsystemAddListenerParams{}
	wantParams.Nqn = "nqn.2014-08.org.nvmexpress:uuid:1630a3a6-5bac-4563-a1a6-d2b0257c282a"
	wantParams.ListenAddress.Trtype = "vfiouser"
	wantParams.ListenAddress.Traddr = filepath.Join(tmpDir, "nvme-1")

	vfiouserSubsysListener := NewVfiouserSubsystemListener(tmpDir)
	gotParams := vfiouserSubsysListener.Params(&pb.NvmeController{
		Spec: &pb.NvmeControllerSpec{
			SubsystemId: &pc.ObjectKey{Value: "nvme-1"},
		},
	}, "nqn.2014-08.org.nvmexpress:uuid:1630a3a6-5bac-4563-a1a6-d2b0257c282a")

	if !reflect.DeepEqual(wantParams, gotParams) {
		t.Errorf("Expect %v, received %v", wantParams, gotParams)
	}
}

func dirExists(dirname string) bool {
	fi, err := os.Stat(dirname)
	return err == nil && fi.IsDir()
}

func TestCreateNvmeController(t *testing.T) {
	expectNotNilOut := server.ProtoClone(testCreateNvmeControllerRequest.NvmeController)
	expectNotNilOut.Spec.NvmeControllerId = -1
	expectNotNilOut.Name = testNvmeControllerName

	tests := map[string]struct {
		jsonRPC                       spdk.JSONRPC
		nonDefaultQmpAddress          string
		ctrlrDirExistsBeforeOperation bool
		ctrlrDirExistsAfterOperation  bool
		buses                         []string

		in      *pb.CreateNvmeControllerRequest
		out     *pb.NvmeController
		errCode codes.Code
		errMsg  string

		mockQmpCalls *mockQmpCalls
	}{
		"valid Nvme controller creation": {
			in:                            testCreateNvmeControllerRequest,
			out:                           expectNotNilOut,
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  true,
			errCode:                       codes.OK,
			errMsg:                        "",
			mockQmpCalls: newMockQmpCalls().
				ExpectAddNvmeController(testNvmeControllerID, testSubsystemID).
				ExpectQueryPci(testNvmeControllerID),
		},
		"spdk failed to create Nvme controller": {
			in:                            testCreateNvmeControllerRequest,
			jsonRPC:                       alwaysFailingJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  false,
			errCode:                       status.Convert(errStub).Code(),
			errMsg:                        status.Convert(errStub).Message(),
		},
		"qemu Nvme controller add failed": {
			in:                            testCreateNvmeControllerRequest,
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  false,
			errCode:                       status.Convert(errAddDeviceFailed).Code(),
			errMsg:                        status.Convert(errAddDeviceFailed).Message(),
			mockQmpCalls: newMockQmpCalls().
				ExpectAddNvmeController(testNvmeControllerID, testSubsystemID).WithErrorResponse(),
		},
		"failed to create monitor": {
			in:                            testCreateNvmeControllerRequest,
			nonDefaultQmpAddress:          "/dev/null",
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  false,
			errCode:                       status.Convert(errMonitorCreation).Code(),
			errMsg:                        status.Convert(errMonitorCreation).Message(),
		},
		"Ctrlr dir already exists": {
			in:                            testCreateNvmeControllerRequest,
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  true,
			errCode:                       status.Convert(errFailedToCreateNvmeDir).Code(),
			errMsg:                        status.Convert(errFailedToCreateNvmeDir).Message(),
		},
		"empty subsystem in request": {
			in: &pb.CreateNvmeControllerRequest{NvmeController: &pb.NvmeController{
				Spec: &pb.NvmeControllerSpec{
					SubsystemId:      nil,
					PcieId:           &pb.PciEndpoint{PhysicalFunction: 1},
					NvmeControllerId: 43,
				},
				Status: &pb.NvmeControllerStatus{
					Active: true,
				},
			}, NvmeControllerId: testNvmeControllerID},
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  false,
			errCode:                       status.Convert(errInvalidSubsystem).Code(),
			errMsg:                        status.Convert(errInvalidSubsystem).Message(),
		},
		"valid Nvme creation with on first bus location": {
			in: &pb.CreateNvmeControllerRequest{NvmeController: &pb.NvmeController{
				Spec: &pb.NvmeControllerSpec{
					SubsystemId:      &pc.ObjectKey{Value: testSubsystemName},
					PcieId:           &pb.PciEndpoint{PhysicalFunction: 1},
					NvmeControllerId: 43,
				},
				Status: &pb.NvmeControllerStatus{
					Active: true,
				},
			}, NvmeControllerId: testNvmeControllerID},
			out: &pb.NvmeController{
				Name: testNvmeControllerName,
				Spec: &pb.NvmeControllerSpec{
					SubsystemId:      &pc.ObjectKey{Value: testSubsystemName},
					PcieId:           &pb.PciEndpoint{PhysicalFunction: 1},
					NvmeControllerId: -1,
				},
				Status: &pb.NvmeControllerStatus{
					Active: true,
				},
			},
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  true,
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			buses:                         []string{"pci.opi.0", "pci.opi.1"},
			mockQmpCalls: newMockQmpCalls().
				ExpectAddNvmeControllerWithAddress(testNvmeControllerID, testSubsystemID, "pci.opi.0", 1).
				ExpectQueryPci(testNvmeControllerID),
		},
		"valid Nvme creation with on second bus location": {
			in:                            testCreateNvmeControllerRequest,
			out:                           expectNotNilOut,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  true,
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			buses:                         []string{"pci.opi.0", "pci.opi.1"},
			mockQmpCalls: newMockQmpCalls().
				ExpectAddNvmeControllerWithAddress(testNvmeControllerID, testSubsystemID, "pci.opi.1", 11).
				ExpectQueryPci(testNvmeControllerID),
		},
		"Nvme creation with physical function goes out of buses": {
			in:      testCreateNvmeControllerRequest,
			out:     nil,
			errCode: status.Convert(errDeviceEndpoint).Code(),
			errMsg:  status.Convert(errDeviceEndpoint).Message(),
			jsonRPC: alwaysSuccessfulJSONRPC,
			buses:   []string{"pci.opi.0"},
		},
		"negative physical function": {
			in: &pb.CreateNvmeControllerRequest{
				NvmeController: &pb.NvmeController{
					Spec: &pb.NvmeControllerSpec{
						SubsystemId: &pc.ObjectKey{Value: testSubsystemName},
						PcieId: &pb.PciEndpoint{
							PhysicalFunction: -1,
						},
						NvmeControllerId: 43,
					},
					Status: &pb.NvmeControllerStatus{
						Active: true,
					},
				}, NvmeControllerId: testNvmeControllerID},
			out:     nil,
			errCode: status.Convert(errDeviceEndpoint).Code(),
			errMsg:  status.Convert(errDeviceEndpoint).Message(),
			jsonRPC: alwaysSuccessfulJSONRPC,
			buses:   []string{"pci.opi.0"},
		},
		"nil pcie endpoint": {
			in: &pb.CreateNvmeControllerRequest{
				NvmeController: &pb.NvmeController{
					Spec: &pb.NvmeControllerSpec{
						SubsystemId:      &pc.ObjectKey{Value: testSubsystemName},
						PcieId:           nil,
						NvmeControllerId: 43,
					},
					Status: &pb.NvmeControllerStatus{
						Active: true,
					},
				}, NvmeControllerId: testNvmeControllerID},
			out:     nil,
			errCode: status.Convert(errNoPcieEndpoint).Code(),
			errMsg:  status.Convert(errNoPcieEndpoint).Message(),
			jsonRPC: alwaysSuccessfulJSONRPC,
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			opiSpdkServer := frontend.NewServer(tt.jsonRPC)
			opiSpdkServer.Nvme.Subsystems[testSubsystemName] = &testSubsystem
			qmpServer := startMockQmpServer(t, tt.mockQmpCalls)
			defer qmpServer.Stop()
			qmpAddress := qmpServer.socketPath
			if tt.nonDefaultQmpAddress != "" {
				qmpAddress = tt.nonDefaultQmpAddress
			}
			kvmServer := NewServer(opiSpdkServer, qmpAddress, qmpServer.testDir, tt.buses)
			kvmServer.timeout = qmplibTimeout
			testCtrlrDir := controllerDirPath(qmpServer.testDir, testSubsystemID)
			if tt.ctrlrDirExistsBeforeOperation &&
				os.Mkdir(testCtrlrDir, os.ModePerm) != nil {
				log.Panicf("Couldn't create ctrlr dir for test")
			}
			request := server.ProtoClone(tt.in)

			out, err := kvmServer.CreateNvmeController(context.Background(), request)

			if !proto.Equal(out, tt.out) {
				t.Error("response: expected", tt.out, "received", out)
			}

			if er, ok := status.FromError(err); ok {
				if er.Code() != tt.errCode {
					t.Error("error code: expected", tt.errCode, "received", er.Code())
				}
				if er.Message() != tt.errMsg {
					t.Error("error message: expected", tt.errMsg, "received", er.Message())
				}
			} else {
				t.Errorf("expected grpc error status")
			}

			if !qmpServer.WereExpectedCallsPerformed() {
				t.Errorf("Not all expected calls were performed")
			}
			ctrlrDirExists := dirExists(testCtrlrDir)
			if tt.ctrlrDirExistsAfterOperation != ctrlrDirExists {
				t.Errorf("Expect controller dir exists %v, got %v", tt.ctrlrDirExistsAfterOperation, ctrlrDirExists)
			}
		})
	}
}

func TestDeleteNvmeController(t *testing.T) {
	tests := map[string]struct {
		jsonRPC              spdk.JSONRPC
		nonDefaultQmpAddress string

		ctrlrDirExistsBeforeOperation bool
		ctrlrDirExistsAfterOperation  bool
		nonEmptyCtrlrDirAfterSpdkCall bool
		noController                  bool
		errCode                       codes.Code
		errMsg                        string

		mockQmpCalls *mockQmpCalls
	}{
		"valid Nvme controller deletion": {
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  false,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			errCode:                       codes.OK,
			errMsg:                        "",
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).
				ExpectNoDeviceQueryPci(),
		},
		"qemu Nvme controller delete failed": {
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  false,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			errCode:                       status.Convert(errDevicePartiallyDeleted).Code(),
			errMsg:                        status.Convert(errDevicePartiallyDeleted).Message(),
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).WithErrorResponse(),
		},
		"spdk failed to delete Nvme controller": {
			jsonRPC:                       alwaysFailingJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  false,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			errCode:                       status.Convert(errDevicePartiallyDeleted).Code(),
			errMsg:                        status.Convert(errDevicePartiallyDeleted).Message(),
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).
				ExpectNoDeviceQueryPci(),
		},
		"failed to create monitor": {
			nonDefaultQmpAddress:          "/dev/null",
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  true,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			errCode:                       status.Convert(errMonitorCreation).Code(),
			errMsg:                        status.Convert(errMonitorCreation).Message(),
		},
		"ctrlr dir is not empty after SPDK call": {
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  true,
			nonEmptyCtrlrDirAfterSpdkCall: true,
			errCode:                       status.Convert(errDevicePartiallyDeleted).Code(),
			errMsg:                        status.Convert(errDevicePartiallyDeleted).Message(),
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).
				ExpectNoDeviceQueryPci(),
		},
		"ctrlr dir does not exist": {
			jsonRPC:                       alwaysSuccessfulJSONRPC,
			ctrlrDirExistsBeforeOperation: false,
			ctrlrDirExistsAfterOperation:  false,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			errCode:                       codes.OK,
			errMsg:                        "",
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).
				ExpectNoDeviceQueryPci(),
		},
		"all communication operations failed": {
			jsonRPC:                       alwaysFailingJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  true,
			nonEmptyCtrlrDirAfterSpdkCall: true,
			errCode:                       status.Convert(errDeviceNotDeleted).Code(),
			errMsg:                        status.Convert(errDeviceNotDeleted).Message(),
			mockQmpCalls: newMockQmpCalls().
				ExpectDeleteNvmeController(testNvmeControllerID).WithErrorResponse(),
		},
		"no controller found": {
			jsonRPC:                       alwaysFailingJSONRPC,
			ctrlrDirExistsBeforeOperation: true,
			ctrlrDirExistsAfterOperation:  true,
			nonEmptyCtrlrDirAfterSpdkCall: false,
			noController:                  true,
			errCode:                       status.Convert(errNoController).Code(),
			errMsg:                        status.Convert(errNoController).Message(),
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			opiSpdkServer := frontend.NewServer(tt.jsonRPC)
			opiSpdkServer.Nvme.Subsystems[testSubsystemName] = &testSubsystem
			if !tt.noController {
				opiSpdkServer.Nvme.Controllers[testNvmeControllerName] =
					server.ProtoClone(testCreateNvmeControllerRequest.NvmeController)
				opiSpdkServer.Nvme.Controllers[testNvmeControllerName].Name = testNvmeControllerID
			}
			qmpServer := startMockQmpServer(t, tt.mockQmpCalls)
			defer qmpServer.Stop()
			qmpAddress := qmpServer.socketPath
			if tt.nonDefaultQmpAddress != "" {
				qmpAddress = tt.nonDefaultQmpAddress
			}
			kvmServer := NewServer(opiSpdkServer, qmpAddress, qmpServer.testDir, nil)
			kvmServer.timeout = qmplibTimeout
			testCtrlrDir := controllerDirPath(qmpServer.testDir, testSubsystemID)
			if tt.ctrlrDirExistsBeforeOperation {
				if err := os.Mkdir(testCtrlrDir, os.ModePerm); err != nil {
					log.Panic(err)
				}

				if tt.nonEmptyCtrlrDirAfterSpdkCall {
					if err := os.Mkdir(filepath.Join(testCtrlrDir, "ctrlr"), os.ModeDir); err != nil {
						log.Panic(err)
					}
				}
			}
			request := server.ProtoClone(testDeleteNvmeControllerRequest)

			_, err := kvmServer.DeleteNvmeController(context.Background(), request)

			if er, ok := status.FromError(err); ok {
				if er.Code() != tt.errCode {
					t.Error("error code: expected", tt.errCode, "received", er.Code())
				}
				if er.Message() != tt.errMsg {
					t.Error("error message: expected", tt.errMsg, "received", er.Message())
				}
			} else {
				t.Errorf("expected grpc error status")
			}

			if !qmpServer.WereExpectedCallsPerformed() {
				t.Errorf("Not all expected calls were performed")
			}
			ctrlrDirExists := dirExists(testCtrlrDir)
			if ctrlrDirExists != tt.ctrlrDirExistsAfterOperation {
				t.Errorf("Expect controller dir exists %v, got %v",
					tt.ctrlrDirExistsAfterOperation, ctrlrDirExists)
			}
		})
	}
}
