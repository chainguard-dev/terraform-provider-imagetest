# Overview

Directory `provision` contains files related to the provisioning of EC2
instances.

# Notes, Discussion

- Execution of scripts is achieved by `go:embed`ing the scripts in 
`commands.go` then implementing a reference to that command in either the 
`*driver.prepareInstance` or `*driver.Run` methods.
- A more flexible future state could be transitioning this to an `embed.FS`,
rather than individual `string`s and a per-script `go:embed` directive, using 
the FS as a namespace to lookup the appropriate scripts to run based on actual 
executing environment details (ex: `Alpine`).

> [!NOTE]
> It's important to note that while a future state could entail an `embed.FS`
> I believe an extreme amount of runtime flexibility must be required for this
> to be a sensible option. Embedding individual scripts (current method) means 
> strong tooling feedback around scripts getting moved or renamed (the 
> `go:embed` directive will produce a compiler error if the script it 
> references does not exist) and this feedback does not exist (without some
> really hacky workarounds) when using an `embed.FS`.
