module github.com/fishbrain/logging-go

replace github.com/bugsnag/bugsnag-go => github.com/fishbrain/bugsnag-go v1.5.4-0.20191022091625-953940cbbbeb

go 1.13

require (
	github.com/DataDog/datadog-go v3.4.0+incompatible // indirect
	github.com/bugsnag/bugsnag-go v1.5.3
	github.com/bugsnag/panicwrap v1.2.0 // indirect
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0 // indirect
	github.com/nsqio/go-nsq v1.1.0
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.9.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.61.0
)
