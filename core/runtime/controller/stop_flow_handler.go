package controller

import (
	"fmt"
	"log"

	"github.com/yuyang0/goflow/core/runtime"
	"github.com/yuyang0/goflow/core/sdk/executor"
)

func StopFlowHandler(response *runtime.Response, request *runtime.Request, ex executor.Executor) error {
	log.Printf("Stopping request %s for flow %s\n", request.FlowName, request.RequestID)

	flowExecutor := executor.CreateFlowExecutor(ex, nil)
	err := flowExecutor.Stop(request.RequestID)
	if err != nil {
		return fmt.Errorf("failed to stop request %s, check if request is active", request.RequestID)
	}

	response.Body = []byte("Successfully stopped request " + request.RequestID)
	return nil
}
