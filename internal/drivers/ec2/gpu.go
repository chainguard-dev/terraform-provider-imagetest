package ec2

import (
	"context"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

// GPU describes an input-configurable GPU which will be applied as a constraint
// against the selectable instances
type GPU struct {
	// The desired GPU kind.
	//
	// Default: `GPUKindNone`
	Kind GPUKind

	// The number of desired GPUs for the instance.
	//
	// If `Kind` is set but this is not, it will default to `1`.
	Count uint8

	// The desired GPU driver version
	Driver string
}

// Describes the kinds of GPUs from which we can choose.
type GPUKind = string

const (
	GPUKindNone GPUKind = "none"
	GPUKindM60  GPUKind = "M60"
	GPUKindK80  GPUKind = "K80"
	GPUKindA10G GPUKind = "A10G"
	GPUKindL4   GPUKind = "L4"
	GPUKindL40S GPUKind = "L40S"
	GPUKindV100 GPUKind = "V100"
	GPUKindA100 GPUKind = "A100"
	GPUKindH100 GPUKind = "H100"
	GPUKindH200 GPUKind = "H200"
)

///////////////////////////////////////////////////////////////////////////////
// Post Filters

func applyGPUFiltersPost(ctx context.Context, d *Driver, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
	if d.GPU.Count == 0 {
		return itypes
	}

	itypes = Post.GPU.Count(ctx, d.GPU.Count, itypes)
	itypes = Post.GPU.Kind(ctx, d.GPU.Kind, itypes)

	return itypes
}

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

func (*filtersGPUPost) Kind(ctx context.Context, kind GPUKind, itypes []types.InstanceTypeInfo) []types.InstanceTypeInfo {
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
