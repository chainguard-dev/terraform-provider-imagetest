// # Overview
//
// ec2 provides machinery for bootstrapping and tearing down AWS EC2 instances.
//
// # Compute (`Instance`)
//
// The primary interface exposed by this package is type `Instance`. All
// `Instance` inputs are abstractions over `ec2.RunInstancesInput`. This allows
// test authors to provide arbitrary desired test environment constraints (CPU
// count, memory capacity, ...) from which this package will analyze all
// instance types it is aware of and select the cheapest which _completely_
// fulfills all inputs.
package ec2

import "github.com/aws/aws-sdk-go-v2/service/ec2"

// For more reading on abstracted inputs, see:
var _ ec2.RunInstancesInput
