package ec2

type Customizations uint8

const (
	CustomAMI Customizations = 1 << iota
	CustomProc
	CustomMemory
	CustomDisk
	CustomGPU
)

func (self Customizations) CustomAMI() bool    { return self&CustomAMI == CustomAMI }
func (self Customizations) CustomProc() bool   { return self&CustomProc == CustomProc }
func (self Customizations) CustomMemory() bool { return self&CustomMemory == CustomMemory }
func (self Customizations) CustomDisk() bool   { return self&CustomDisk == CustomDisk }
func (self Customizations) CustomGPU() bool    { return self&CustomGPU == CustomGPU }
