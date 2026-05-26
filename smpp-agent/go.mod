module vendel-smpp-agent

go 1.26.1

require (
	github.com/JimScope/vendel/agent-core v0.0.0
	github.com/linxGnu/gosmpp v0.3.1
	golang.org/x/time v0.15.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/orcaman/concurrent-map/v2 v2.0.1 // indirect
	golang.org/x/exp v0.0.0-20240604190554-fc45aab8b7f8 // indirect
	golang.org/x/text v0.20.0 // indirect
)

replace github.com/JimScope/vendel/agent-core => ../agent-core
