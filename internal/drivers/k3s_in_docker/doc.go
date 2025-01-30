// k3sindocker is a driver that runs each test in a pod within a k3s cluster
// run in docker. Each test pod is run to completion depending on the
// entrypoint/cmd combination of the test image. The test pod has essentially
// cluster-admin to the cluster it is running on. This means tests authored
// with this driver operate in an environment that is within the network
// boundary of a pod within the cluster, where the default KUBECONFIG is
// pre-wired with cluster-admin scoped. This means accessing network endpoints
// can be achieved directly by addressing the in cluster endpoints
// (*.svc.cluster.local)
package k3sindocker
