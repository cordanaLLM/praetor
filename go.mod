module github.com/cordanallm/praetor

go 1.27.0

require (
	github.com/golusoris/golusoris/core v0.9.0
	golang.org/x/mod v0.41.0
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

// TODO(core/v0.9.0): drop this replace once golusoris tags core/v0.9.0 and the module proxy serves it.
replace github.com/golusoris/golusoris/core => C:/Users/ms/dev/golusoris/golusoris/core
