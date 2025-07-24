package filter

import (
	"context"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type filtersStoragePost struct{}

func (*filtersStoragePost) DiskCount(ctx context.Context, count uint8, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)
	if count == 0 {
		log.Debug("skipping disk count evaluation")
		return itypes
	}
	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)
		if typ.InstanceStorageInfo == nil {
			log.Debug("instance has no storage info")
			return true
		}
		log.Debug("evaluating instance disk count", "have", len(typ.InstanceStorageInfo.Disks), "want", count)
		return len(typ.InstanceStorageInfo.Disks) < int(count)
	})
}

func (*filtersStoragePost) DiskCapacity(ctx context.Context, capacity uint16, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)
	if capacity == 0 {
		log.Debug("skipping disk capacity evaluation")
		return itypes
	}
	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)
		if typ.InstanceStorageInfo == nil {
			log.Debug("instance has no storage info")
			return true
		}
		for _, disk := range typ.InstanceStorageInfo.Disks {
			log.Debug("evaluating disk capacity", "have", *disk.SizeInGB, "want", capacity)
			if *disk.SizeInGB >= int64(capacity) {
				return false
			}
		}
		return true
	})
}
