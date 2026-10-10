module github.com/cordanaLLM/praetor/tools/schemacheck

go 1.27

require (
	github.com/cordanaLLM/praetor v0.0.0
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/text v0.42.0 // indirect

replace github.com/cordanaLLM/praetor => ../..
