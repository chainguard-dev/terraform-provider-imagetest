package filter

import (
	"context"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type filtersGPUPost struct{}

func (*filtersGPUPost) Count(ctx context.Context, n uint8, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	log := clog.FromContext(ctx)

	if n == 0 {
		log.Debug("skipping GPU count evaluation")
		return itypes
	}

	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)

		if typ.GpuInfo == nil {
			log.Debug("instance has no attached GPU")
			return true
		} else {
			log.Debug("instance has attached GPU")
		}

		for _, gpu := range typ.GpuInfo.Gpus {
			if *gpu.Count >= int32(n) {
				return false
			}
		}

		return true
	})
}

func (*filtersGPUPost) Kind(ctx context.Context, kind string, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	m := new(sync.Mutex)
	m.Lock()

	log := clog.FromContext(ctx)

	if kind == "" {
		return itypes
	}

	return slices.DeleteFunc(itypes, func(typ types.InstanceTypeInfo) bool {
		log := log.With("instance_type", typ.InstanceType)
		if typ.GpuInfo == nil {
			log.Debug("instance has no attached GPU")
			return true
		}

		for _, gpu := range typ.GpuInfo.Gpus {
			if *gpu.Name == kind {
				return false
			}
		}

		return true
	})
}
