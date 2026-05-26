module vendel-modem-agent

go 1.26.1

require (
	github.com/JimScope/vendel/agent-core v0.0.0
	github.com/xlab/at v1.0.1-0.20260329105545-b341cce335ba
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/JimScope/vendel/agent-core => ../agent-core
