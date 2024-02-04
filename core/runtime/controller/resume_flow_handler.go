package controller

import (
	"fmt"
	"log"

	"github.com/yuyang0/goflow/core/runtime"

	"github.com/yuyang0/goflow/core/sdk/executor"
)

func ResumeFlowHandler(response *runtime.Response, request *runtime.Request, ex executor.Executor) error {
	log.Printf("Resuming flow %s for request %s\n", request.FlowName, request.RequestID)

	flowExecutor := executor.CreateFlowExecutor(ex, nil)
	err := flowExecutor.Resume(request.RequestID)
	if err != nil {
		return fmt.Errorf("failed to resume request %s, check if request is active", request.RequestID)
	}

	response.Body = []byte("Successfully resumed request " + request.RequestID)
	return nil
}
