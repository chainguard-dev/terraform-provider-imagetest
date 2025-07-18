package filter

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Pre filters are handled by the AWS backend as part of the
// `DescribeInstanceTypes` request.
var Pre struct {
	General filtersGeneralPre
	Proc    filtersProcPre
	Memory  filtersMemoryPre
	Storage filtersStoragePre
}

// newFilterPre produces a generic `ec2/types.newFilterPre`, handling the string-to-string
// -pointer conversion and adding a bit of composability by moving the values
// definition to a variadic arg.
func newFilterPre(name string, values ...string) types.Filter {
	return types.Filter{
		Name:   aws.String(name),
		Values: values,
	}
}

// !
// For posterity, below are all the unimplemented filters supported when
// querying EC2 instances.
// !

// CPU Features
//
// processor-info.sustained-clock-speed-in-ghz - The CPU clock speed, in GHz.
// processor-info.supported-features - The supported CPU features (amd-sev-snp).

// Misc Instance Features
//
// auto-recovery-supported - Indicates whether Amazon CloudWatch action based recovery is supported (true | false).
// bare-metal - Indicates whether it is a bare metal instance type (true | false).
// burstable-performance-supported - Indicates whether the instance type is a burstable performance T instance type (true | false).
// dedicated-hosts-supported - Indicates whether the instance type supports Dedicated Hosts. (true | false)
// hibernation-supported - Indicates whether On-Demand hibernation is supported (true | false).
// reboot-migration-support - Indicates whether enabling reboot migration is supported (supported | unsupported).

// EBS
//
// ebs-info.ebs-optimized-info.baseline-bandwidth-in-mbps - The baseline bandwidth performance for an EBS-optimized instance type, in Mbps.
// ebs-info.ebs-optimized-info.baseline-iops - The baseline input/output storage operations per second for an EBS-optimized instance type.
// ebs-info.ebs-optimized-info.baseline-throughput-in-mbps - The baseline throughput performance for an EBS-optimized instance type, in MB/s.
// ebs-info.ebs-optimized-info.maximum-bandwidth-in-mbps - The maximum bandwidth performance for an EBS-optimized instance type, in Mbps.
// ebs-info.ebs-optimized-info.maximum-iops - The maximum input/output storage operations per second for an EBS-optimized instance type.
// ebs-info.ebs-optimized-info.maximum-throughput-in-mbps - The maximum throughput performance for an EBS-optimized instance type, in MB/s.
// ebs-info.ebs-optimized-support - Indicates whether the instance type is EBS-optimized (supported | unsupported | default).
// ebs-info.encryption-support - Indicates whether EBS encryption is supported (supported | unsupported).
// ebs-info.nvme-support - Indicates whether non-volatile memory express (NVMe) is supported for EBS volumes (required | supported | unsupported).

// Networking
//
// network-info.bandwidth-weightings - For instances that support bandwidth weighting to boost performance (default, vpc-1, ebs-1).
// network-info.efa-info.maximum-efa-interfaces - The maximum number of Elastic Fabric Adapters (EFAs) per instance.
// network-info.efa-supported - Indicates whether the instance type supports Elastic Fabric Adapter (EFA) (true | false).
// network-info.ena-support - Indicates whether Elastic Network Adapter (ENA) is supported or required (required | supported | unsupported).
// network-info.flexible-ena-queues-support - Indicates whether an instance supports flexible ENA queues (supported | unsupported).
// network-info.encryption-in-transit-supported - Indicates whether the instance type automatically encrypts in-transit traffic between instances (true | false).
// network-info.ipv4-addresses-per-interface - The maximum number of private IPv4 addresses per network interface.
// network-info.ipv6-addresses-per-interface - The maximum number of private IPv6 addresses per network interface.
// network-info.ipv6-supported - Indicates whether the instance type supports IPv6 (true | false).
// network-info.maximum-network-cards - The maximum number of network cards per instance.
// network-info.maximum-network-interfaces - The maximum number of network interfaces per instance.
// network-info.network-performance - The network performance (for example, "25 Gigabit").

// Nitro & TPM
//
// nitro-enclaves-support - Indicates whether Nitro Enclaves is supported (supported | unsupported).
// nitro-tpm-support - Indicates whether NitroTPM is supported (supported | unsupported).
// nitro-tpm-info.supported-versions - The supported NitroTPM version (2.0).
