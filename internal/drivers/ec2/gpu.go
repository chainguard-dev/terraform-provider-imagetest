package ec2

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
