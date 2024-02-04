package main

import (
	"fmt"

	"github.com/s8sg/goflow/samples/condition"
	"github.com/s8sg/goflow/samples/loop"
	"github.com/s8sg/goflow/samples/myflow"
	"github.com/s8sg/goflow/samples/parallel"
	"github.com/s8sg/goflow/samples/serial"
	"github.com/s8sg/goflow/samples/single"
	"github.com/s8sg/goflow/types"

	goflow "github.com/s8sg/goflow/v1"
)

func main() {
	fs := &goflow.FlowService{
		Port: 8080,
		RedisCfg: types.RedisConfig{
			Addr:     "localhost:6379",
			Username: "goflow",
			Password: "redis",
		},
		OpenTraceUrl:      "localhost:5775",
		WorkerConcurrency: 5,
		EnableMonitoring:  true,
		DebugEnabled:      true,
	}
	fs.Register("single", single.DefineWorkflow)
	fs.Register("serial", serial.DefineWorkflow)
	fs.Register("parallel", parallel.DefineWorkflow)
	fs.Register("condition", condition.DefineWorkflow)
	fs.Register("loop", loop.DefineWorkflow)
	fs.Register("myflow", myflow.DefineWorkflow)
	fmt.Println(fs.Start())
}
