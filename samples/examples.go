package main

import (
	"fmt"

	"github.com/yuyang0/goflow/samples/condition"
	"github.com/yuyang0/goflow/samples/loop"
	"github.com/yuyang0/goflow/samples/myflow"
	"github.com/yuyang0/goflow/samples/parallel"
	"github.com/yuyang0/goflow/samples/serial"
	"github.com/yuyang0/goflow/samples/single"
	"github.com/yuyang0/goflow/types"

	goflow "github.com/yuyang0/goflow/v1"
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
