// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2022-2023 Dell Inc, or its subsidiaries.
// Copyright (C) 2023 Intel Corporation

// Package backend implememnts the BackEnd APIs (network facing) of the storage Server
package backend

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"

	"github.com/opiproject/gospdk/spdk"
	pc "github.com/opiproject/opi-api/common/v1/gen/go"
	pb "github.com/opiproject/opi-api/storage/v1alpha1/gen/go"
	"github.com/opiproject/opi-spdk-bridge/pkg/server"

	"github.com/google/uuid"
	"go.einride.tech/aip/fieldbehavior"
	"go.einride.tech/aip/fieldmask"
	"go.einride.tech/aip/resourceid"
	"go.einride.tech/aip/resourcename"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func sortNullDebugs(nullDebugs []*pb.NullDebug) {
	sort.Slice(nullDebugs, func(i int, j int) bool {
		return nullDebugs[i].Name < nullDebugs[j].Name
	})
}

// CreateNullDebug creates a Null Debug instance
func (s *Server) CreateNullDebug(_ context.Context, in *pb.CreateNullDebugRequest) (*pb.NullDebug, error) {
	log.Printf("CreateNullDebug: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// see https://google.aip.dev/133#user-specified-ids
	resourceID := resourceid.NewSystemGenerated()
	if in.NullDebugId != "" {
		err := resourceid.ValidateUserSettable(in.NullDebugId)
		if err != nil {
			log.Printf("error: %v", err)
			return nil, err
		}
		log.Printf("client provided the ID of a resource %v, ignoring the name field %v", in.NullDebugId, in.NullDebug.Name)
		resourceID = in.NullDebugId
	}
	in.NullDebug.Name = server.ResourceIDToVolumeName(resourceID)
	// idempotent API when called with same key, should return same object
	volume, ok := s.Volumes.NullVolumes[in.NullDebug.Name]
	if ok {
		log.Printf("Already existing NullDebug with id %v", in.NullDebug.Name)
		return volume, nil
	}
	// not found, so create a new one
	params := spdk.BdevNullCreateParams{
		Name:      resourceID,
		BlockSize: 512,
		NumBlocks: 64,
	}
	var result spdk.BdevNullCreateResult
	err := s.rpc.Call("bdev_null_create", &params, &result)
	if err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	log.Printf("Received from SPDK: %v", result)
	if result == "" {
		msg := fmt.Sprintf("Could not create Null Dev: %s", params.Name)
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	response := server.ProtoClone(in.NullDebug)
	s.Volumes.NullVolumes[in.NullDebug.Name] = response
	log.Printf("CreateNullDebug: Sending to client: %v", response)
	return response, nil
}

// DeleteNullDebug deletes a Null Debug instance
func (s *Server) DeleteNullDebug(_ context.Context, in *pb.DeleteNullDebugRequest) (*emptypb.Empty, error) {
	log.Printf("DeleteNullDebug: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// Validate that a resource name conforms to the restrictions outlined in AIP-122.
	if err := resourcename.Validate(in.Name); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// fetch object from the database
	volume, ok := s.Volumes.NullVolumes[in.Name]
	if !ok {
		if in.AllowMissing {
			return &emptypb.Empty{}, nil
		}
		err := status.Errorf(codes.NotFound, "unable to find key %s", in.Name)
		log.Printf("error: %v", err)
		return nil, err
	}
	resourceID := path.Base(volume.Name)
	params := spdk.BdevNullDeleteParams{
		Name: resourceID,
	}
	var result spdk.BdevNullDeleteResult
	err := s.rpc.Call("bdev_null_delete", &params, &result)
	if err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	log.Printf("Received from SPDK: %v", result)
	if !result {
		msg := fmt.Sprintf("Could not delete Null Dev: %s", params.Name)
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	delete(s.Volumes.NullVolumes, volume.Name)
	return &emptypb.Empty{}, nil
}

// UpdateNullDebug updates a Null Debug instance
func (s *Server) UpdateNullDebug(_ context.Context, in *pb.UpdateNullDebugRequest) (*pb.NullDebug, error) {
	log.Printf("UpdateNullDebug: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// Validate that a resource name conforms to the restrictions outlined in AIP-122.
	if err := resourcename.Validate(in.NullDebug.Name); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// fetch object from the database
	volume, ok := s.Volumes.NullVolumes[in.NullDebug.Name]
	if !ok {
		if in.AllowMissing {
			log.Printf("Got AllowMissing, create a new resource, don't return error when resource not found")
			params := spdk.BdevNullCreateParams{
				Name:      path.Base(in.NullDebug.Name),
				BlockSize: 512,
				NumBlocks: 64,
			}
			var result spdk.BdevNullCreateResult
			err := s.rpc.Call("bdev_null_create", &params, &result)
			if err != nil {
				log.Printf("error: %v", err)
				return nil, err
			}
			log.Printf("Received from SPDK: %v", result)
			if result == "" {
				msg := fmt.Sprintf("Could not create Null Dev: %s", params.Name)
				log.Print(msg)
				return nil, status.Errorf(codes.InvalidArgument, msg)
			}
			response := server.ProtoClone(in.NullDebug)
			s.Volumes.NullVolumes[in.NullDebug.Name] = response
			log.Printf("CreateNullDebug: Sending to client: %v", response)
			return response, nil
		}
		err := status.Errorf(codes.NotFound, "unable to find key %s", in.NullDebug.Name)
		log.Printf("error: %v", err)
		return nil, err
	}
	resourceID := path.Base(volume.Name)
	// update_mask = 2
	if err := fieldmask.Validate(in.UpdateMask, in.NullDebug); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	params1 := spdk.BdevNullDeleteParams{
		Name: resourceID,
	}
	var result1 spdk.BdevNullDeleteResult
	err1 := s.rpc.Call("bdev_null_delete", &params1, &result1)
	if err1 != nil {
		log.Printf("error: %v", err1)
		return nil, err1
	}
	log.Printf("Received from SPDK: %v", result1)
	if !result1 {
		msg := fmt.Sprintf("Could not delete Null Dev: %s", params1.Name)
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	params2 := spdk.BdevNullCreateParams{
		Name:      resourceID,
		BlockSize: 512,
		NumBlocks: 64,
	}
	var result2 spdk.BdevNullCreateResult
	err2 := s.rpc.Call("bdev_null_create", &params2, &result2)
	if err2 != nil {
		log.Printf("error: %v", err2)
		return nil, err2
	}
	log.Printf("Received from SPDK: %v", result2)
	if result2 == "" {
		msg := fmt.Sprintf("Could not create Null Dev: %s", params2.Name)
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	response := server.ProtoClone(in.NullDebug)
	s.Volumes.NullVolumes[in.NullDebug.Name] = response
	return response, nil
}

// ListNullDebugs lists Null Debug instances
func (s *Server) ListNullDebugs(_ context.Context, in *pb.ListNullDebugsRequest) (*pb.ListNullDebugsResponse, error) {
	log.Printf("ListNullDebugs: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// fetch object from the database
	size, offset, perr := server.ExtractPagination(in.PageSize, in.PageToken, s.Pagination)
	if perr != nil {
		log.Printf("error: %v", perr)
		return nil, perr
	}
	var result []spdk.BdevGetBdevsResult
	err := s.rpc.Call("bdev_get_bdevs", nil, &result)
	if err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	log.Printf("Received from SPDK: %v", result)
	token := ""
	log.Printf("Limiting result len(%d) to [%d:%d]", len(result), offset, size)
	result, hasMoreElements := server.LimitPagination(result, offset, size)
	if hasMoreElements {
		token = uuid.New().String()
		s.Pagination[token] = offset + size
	}
	Blobarray := make([]*pb.NullDebug, len(result))
	for i := range result {
		r := &result[i]
		Blobarray[i] = &pb.NullDebug{Name: r.Name, Uuid: &pc.Uuid{Value: r.UUID}, BlockSize: r.BlockSize, BlocksCount: r.NumBlocks}
	}
	sortNullDebugs(Blobarray)
	return &pb.ListNullDebugsResponse{NullDebugs: Blobarray, NextPageToken: token}, nil
}

// GetNullDebug gets a a Null Debug instance
func (s *Server) GetNullDebug(_ context.Context, in *pb.GetNullDebugRequest) (*pb.NullDebug, error) {
	log.Printf("GetNullDebug: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// Validate that a resource name conforms to the restrictions outlined in AIP-122.
	if err := resourcename.Validate(in.Name); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// fetch object from the database
	volume, ok := s.Volumes.NullVolumes[in.Name]
	if !ok {
		err := status.Errorf(codes.NotFound, "unable to find key %s", in.Name)
		log.Printf("error: %v", err)
		return nil, err
	}
	resourceID := path.Base(volume.Name)
	params := spdk.BdevGetBdevsParams{
		Name: resourceID,
	}
	var result []spdk.BdevGetBdevsResult
	err := s.rpc.Call("bdev_get_bdevs", &params, &result)
	if err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	log.Printf("Received from SPDK: %v", result)
	if len(result) != 1 {
		msg := fmt.Sprintf("expecting exactly 1 result, got %d", len(result))
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	return &pb.NullDebug{Name: result[0].Name, Uuid: &pc.Uuid{Value: result[0].UUID}, BlockSize: result[0].BlockSize, BlocksCount: result[0].NumBlocks}, nil
}

// NullDebugStats gets a Null Debug instance stats
func (s *Server) NullDebugStats(_ context.Context, in *pb.NullDebugStatsRequest) (*pb.NullDebugStatsResponse, error) {
	log.Printf("NullDebugStats: Received from client: %v", in)
	// check required fields
	if err := fieldbehavior.ValidateRequiredFields(in); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// Validate that a resource name conforms to the restrictions outlined in AIP-122.
	if err := resourcename.Validate(in.Handle.Value); err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	// fetch object from the database
	volume, ok := s.Volumes.NullVolumes[in.Handle.Value]
	if !ok {
		err := status.Errorf(codes.NotFound, "unable to find key %s", in.Handle.Value)
		log.Printf("error: %v", err)
		return nil, err
	}
	resourceID := path.Base(volume.Name)
	params := spdk.BdevGetIostatParams{
		Name: resourceID,
	}
	// See https://mholt.github.io/json-to-go/
	var result spdk.BdevGetIostatResult
	err := s.rpc.Call("bdev_get_iostat", &params, &result)
	if err != nil {
		log.Printf("error: %v", err)
		return nil, err
	}
	log.Printf("Received from SPDK: %v", result)
	if len(result.Bdevs) != 1 {
		msg := fmt.Sprintf("expecting exactly 1 result, got %d", len(result.Bdevs))
		log.Print(msg)
		return nil, status.Errorf(codes.InvalidArgument, msg)
	}
	return &pb.NullDebugStatsResponse{Stats: &pb.VolumeStats{
		ReadBytesCount:    int32(result.Bdevs[0].BytesRead),
		ReadOpsCount:      int32(result.Bdevs[0].NumReadOps),
		WriteBytesCount:   int32(result.Bdevs[0].BytesWritten),
		WriteOpsCount:     int32(result.Bdevs[0].NumWriteOps),
		UnmapBytesCount:   int32(result.Bdevs[0].BytesUnmapped),
		UnmapOpsCount:     int32(result.Bdevs[0].NumUnmapOps),
		ReadLatencyTicks:  int32(result.Bdevs[0].ReadLatencyTicks),
		WriteLatencyTicks: int32(result.Bdevs[0].WriteLatencyTicks),
		UnmapLatencyTicks: int32(result.Bdevs[0].UnmapLatencyTicks),
	}}, nil
}
