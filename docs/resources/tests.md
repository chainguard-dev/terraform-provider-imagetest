---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "imagetest_tests Resource - terraform-provider-imagetest"
subcategory: ""
description: |-
  
---

# imagetest_tests (Resource)





<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `driver` (String) The driver to use for the test suite. Only one driver can be used at a time.
- `images` (Map of String) Images to use for the test suite.

### Optional

- `drivers` (Attributes) The resource specific driver configuration. This is merged with the provider scoped drivers configuration. (see [below for nested schema](#nestedatt--drivers))
- `name` (String) The name of the test. If one is not provided, a random name will be generated.
- `tests` (Attributes List) An ordered list of test suites to run (see [below for nested schema](#nestedatt--tests))

### Read-Only

- `id` (String) The unique identifier for the test. If a name is provided, this will be the name appended with a random suffix.

<a id="nestedatt--drivers"></a>
### Nested Schema for `drivers`

Optional:

- `docker_in_docker` (Attributes) The docker_in_docker driver (see [below for nested schema](#nestedatt--drivers--docker_in_docker))
- `k3s_in_docker` (Attributes) The k3s_in_docker driver (see [below for nested schema](#nestedatt--drivers--k3s_in_docker))

<a id="nestedatt--drivers--docker_in_docker"></a>
### Nested Schema for `drivers.docker_in_docker`

Optional:

- `image_ref` (String) The image reference to use for the docker-in-docker driver


<a id="nestedatt--drivers--k3s_in_docker"></a>
### Nested Schema for `drivers.k3s_in_docker`

Optional:

- `cni` (Boolean) Enable the CNI plugin
- `metrics_server` (Boolean) Enable the metrics server
- `network_policy` (Boolean) Enable the network policy
- `traefik` (Boolean) Enable the traefik ingress controller



<a id="nestedatt--tests"></a>
### Nested Schema for `tests`

Required:

- `image` (String) The image reference to use as the base image for the test.
- `name` (String) The name of the test

Optional:

- `content` (Attributes List) The content to use for the test (see [below for nested schema](#nestedatt--tests--content))
- `envs` (Map of String) Environment variables to set on the test container. These will overwrite the environment variables set in the image's config on conflicts.

<a id="nestedatt--tests--content"></a>
### Nested Schema for `tests.content`

Required:

- `source` (String) The source path to use for the test

Optional:

- `target` (String) The target path to use for the test