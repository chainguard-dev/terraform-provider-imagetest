// dockerindocker is a driver that runs each test container in its _own_ dind
// sandbox. Each test container is created as a new image, with the base layer
// containing the dind image, and subsequent layers containing the test
// container. Mapped out, the layers look like:
//
//	0: cgr.dev/chainguard-private/docker-dind:latest
//	1: imagetest created layer, with the appropriate test content and apk dependencies
//
// Things are done this way to ensure the tests that run _feel_ like they are
// simply in an environment with docker installed, while also ensuring they are
// portable to other drivers, such as docker-in-a-vm.
package dockerindocker
