# `imagetest` Design

This doc outlines in more detail the user facing design of the provider, and
how that translates to the decisions made on the backend. It is not meant to be
a complete architectural design doc.

## Goals

The goal of the `imagetest` provider is to enhance the testability of the
images tests in [Chainguard Images](https://github.com/chainguard-dev/images).
It achieves this by breaking the testing suites into a handful of terraform
data/resource primitives, which under the hood, operate in ways that enhance
the testability of each image.

## Assumptions

At a high level, the current `images` terraform project is structured as follows:

- "globals": defined in `./main.tf`
- "images": defined in `./image/$image/main.tf`

The valid operating models of `Chainguard Images` is:

```bash
# the "mono" apply
terraform apply

# the image based targeted apply
terraform apply -target 'module.image'
```

The `imagetest` provider is designed to work under these existing assumptions.
