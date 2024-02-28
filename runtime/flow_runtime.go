package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/alphadose/haxmap"
	"github.com/jasonlvhit/gocron"
	"github.com/redis/go-redis/v9"
	"github.com/rs/xid"
	"github.com/yuyang0/goflow/core/runtime"
	"github.com/yuyang0/goflow/core/runtime/controller"
	"github.com/yuyang0/goflow/core/sdk"
	"github.com/yuyang0/goflow/core/sdk/executor"
	"github.com/yuyang0/goflow/core/sdk/exporter"
	"github.com/yuyang0/goflow/eventhandler"
	log2 "github.com/yuyang0/goflow/log"
	"github.com/yuyang0/goflow/types"
)

type FlowRuntime struct {
	Flows                   *haxmap.Map[string, FlowDefinitionHandler]
	OpenTracingUrl          string
	RedisCfg                types.RedisConfig
	stateStore              sdk.StateStore
	DataStore               sdk.DataStore
	Logger                  sdk.Logger
	Concurrency             int
	ServerPort              int
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	RequestAuthSharedSecret string
	RequestAuthEnabled      bool
	EnableMonitoring        bool
	RetryQueueCount         int
	DebugEnabled            bool
	workerMode              atomic.Bool

	eventHandler sdk.EventHandler

	taskQueues    map[string]rmq.Queue
	srv           *http.Server
	rdb           *redis.Client
	rmqConnection rmq.Connection
}

type Worker struct {
	mu          sync.Mutex
	ID          string   `json:"id"`
	Flows       []string `json:"flows"`
	Concurrency int      `json:"concurrency"`
}

type Task struct {
	FlowName    string              `json:"flow_name"`
	RequestID   string              `json:"request_id"`
	Body        string              `json:"body"`
	Header      map[string][]string `json:"header"`
	RawQuery    string              `json:"raw_query"`
	Query       map[string][]string `json:"query"`
	RequestType string              `json:"request_type"`
}

const (
	InternalRequestQueueInitial = "goflow-internal-request"
	FlowKeyInitial              = "goflow-flow"
	WorkerKeyInitial            = "goflow-worker"

	GoFlowRegisterInterval = 4
	RDBKeyTimeOut          = 10

	PartialRequest = "PARTIAL"
	NewRequest     = "NEW"
	PauseRequest   = "PAUSE"
	ResumeRequest  = "RESUME"
	StopRequest    = "STOP"
)

func (fRuntime *FlowRuntime) Init() error {
	var err error

	fRuntime.rdb = fRuntime.RedisCfg.NewRedisClient()

	fRuntime.stateStore, err = initStateStore(&fRuntime.RedisCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize the StateStore, %v", err)
	}

	if fRuntime.DataStore == nil {
		fRuntime.DataStore, err = initDataStore(&fRuntime.RedisCfg)
		if err != nil {
			return fmt.Errorf("failed to initialize the StateStore, %v", err)
		}
	}

	fRuntime.rmqConnection, err = OpenConnectionV2("goflow", &fRuntime.RedisCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate rmq connection, error %v", err)
	}

	if fRuntime.Logger == nil {
		fRuntime.Logger = &log2.StdErrLogger{}
	}

	fRuntime.eventHandler = &eventhandler.GoFlowEventHandler{
		TraceURI: fRuntime.OpenTracingUrl,
	}

	return nil
}

func (fRuntime *FlowRuntime) CreateExecutor(req *runtime.Request) (executor.Executor, error) {
	flowHandler, ok := fRuntime.Flows.Get(req.FlowName)
	if !ok {
		return nil, fmt.Errorf("could not find handler for flow %s", req.FlowName)
	}
	ex := &FlowExecutor{
		StateStore:              fRuntime.stateStore,
		RequestAuthSharedSecret: fRuntime.RequestAuthSharedSecret,
		RequestAuthEnabled:      fRuntime.RequestAuthEnabled,
		DataStore:               fRuntime.DataStore,
		EventHandler:            fRuntime.eventHandler,
		EnableMonitoring:        fRuntime.EnableMonitoring,
		Handler:                 flowHandler,
		Logger:                  fRuntime.Logger,
		Runtime:                 fRuntime,
		IsLoggingEnabled:        fRuntime.DebugEnabled,
	}
	err := ex.Init(req)
	return ex, err
}

// Register flows to the runtime
// If the flow is already registered, it returns an error
func (fRuntime *FlowRuntime) Register(flows map[string]FlowDefinitionHandler) error {
	if reflect.ValueOf(fRuntime.rmqConnection).IsNil() {
		return fmt.Errorf("unable to register flows, rmq connection not initialized")
	}

	if len(flows) == 0 {
		return nil
	}

	var flowNames []string
	for flowName := range flows {
		if _, ok := fRuntime.Flows.Get(flowName); ok {
			return fmt.Errorf("flow %s already registered", flowName)
		}

		flowNames = append(flowNames, flowName)
	}

	// register flows to runtime
	for flowName, flowHandler := range flows {
		fRuntime.Flows.Set(flowName, flowHandler)
	}

	// initialize task queues when in worker mode
	if fRuntime.workerMode.Load() {
		err := fRuntime.initializeTaskQueues(&fRuntime.rmqConnection, fRuntime.Flows)
		if err != nil {
			return fmt.Errorf(fmt.Sprintf("failed to initialize task queues for flows %v, error %v", flowNames, err))
		}
	}

	fRuntime.Logger.Log(fmt.Sprintf("[goflow] queue workers for flows %v started successfully", flowNames))

	return nil
}

// EnterWorkerMode put the runtime into worker mode
func (fRuntime *FlowRuntime) EnterWorkerMode() error {
	if reflect.ValueOf(fRuntime.rmqConnection).IsNil() {
		return fmt.Errorf("unable to enter worker mode, rmq connection not initialized")
	}

	if fRuntime.workerMode.Load() {
		// already in worker mode
		return nil
	}
	fRuntime.workerMode.Store(true)

	err := fRuntime.initializeTaskQueues(&fRuntime.rmqConnection, fRuntime.Flows)
	if err != nil {
		return fmt.Errorf("failed to enter worker mode, error: " + err.Error())
	}

	return nil
}

// ExitWorkerMode take the runtime out of worker mode
func (fRuntime *FlowRuntime) ExitWorkerMode() error {
	if reflect.ValueOf(fRuntime.rmqConnection).IsNil() {
		return nil
	}

	if !fRuntime.workerMode.Load() {
		// already not in worker mode
		return nil
	}
	fRuntime.workerMode.Store(false)

	err := fRuntime.cleanTaskQueues()
	if err != nil {
		return fmt.Errorf("failed to exit worker mode, error: " + err.Error())
	}

	return nil
}

// OpenConnection opens and returns a new connection
func OpenConnectionV2(tag string, cfg *types.RedisConfig, errChan chan<- error) (rmq.Connection, error) {
	redisClient := cfg.NewRedisClient()
	return rmq.OpenConnectionWithRedisClient(tag, redisClient, errChan)
}

func (fRuntime *FlowRuntime) Execute(flowName string, request *runtime.Request) error {

	connection, err := OpenConnectionV2("goflow", &fRuntime.RedisCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate connection, error %v", err)
	}
	taskQueue, err := connection.OpenQueue(fRuntime.internalRequestQueueId(flowName))
	if err != nil {
		return fmt.Errorf("failed to get queue, error %v", err)
	}

	data, _ := json.Marshal(&Task{
		FlowName:    flowName,
		RequestID:   request.RequestID,
		Body:        string(request.Body),
		Header:      request.Header,
		RawQuery:    request.RawQuery,
		Query:       request.Query,
		RequestType: NewRequest,
	})
	err = taskQueue.PublishBytes(data)
	if err != nil {
		return fmt.Errorf("failed to publish task, error %v", err)
	}
	return nil
}

func (fRuntime *FlowRuntime) Pause(flowName string, request *runtime.Request) error {
	connection, err := OpenConnectionV2("goflow", &fRuntime.RedisCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate connection, error %v", err)
	}
	taskQueue, err := connection.OpenQueue(fRuntime.internalRequestQueueId(flowName))
	if err != nil {
		return fmt.Errorf("failed to get queue, error %v", err)
	}
	data, _ := json.Marshal(&Task{
		FlowName:    flowName,
		RequestID:   request.RequestID,
		Body:        string(request.Body),
		Header:      request.Header,
		RawQuery:    request.RawQuery,
		Query:       request.Query,
		RequestType: PauseRequest,
	})
	err = taskQueue.PublishBytes(data)
	if err != nil {
		return fmt.Errorf("failed to publish task, error %v", err)
	}
	return nil
}

func (fRuntime *FlowRuntime) Stop(flowName string, request *runtime.Request) error {
	connection, err := OpenConnectionV2("goflow", &fRuntime.RedisCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate connection, error %v", err)
	}
	taskQueue, err := connection.OpenQueue(fRuntime.internalRequestQueueId(flowName))
	if err != nil {
		return fmt.Errorf("failed to get queue, error %v", err)
	}
	data, _ := json.Marshal(&Task{
		FlowName:    flowName,
		RequestID:   request.RequestID,
		Body:        string(request.Body),
		Header:      request.Header,
		RawQuery:    request.RawQuery,
		Query:       request.Query,
		RequestType: StopRequest,
	})
	err = taskQueue.PublishBytes(data)
	if err != nil {
		return fmt.Errorf("failed to publish task, error %v", err)
	}
	return nil
}

func (fRuntime *FlowRuntime) Resume(flowName string, request *runtime.Request) error {
	connection, err := OpenConnectionV2("goflow", &fRuntime.RedisCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initiate connection, error %v", err)
	}
	taskQueue, err := connection.OpenQueue(fRuntime.internalRequestQueueId(flowName))
	if err != nil {
		return fmt.Errorf("failed to get queue, error %v", err)
	}
	data, _ := json.Marshal(&Task{
		FlowName:    flowName,
		RequestID:   request.RequestID,
		Body:        string(request.Body),
		Header:      request.Header,
		RawQuery:    request.RawQuery,
		Query:       request.Query,
		RequestType: ResumeRequest,
	})
	err = taskQueue.PublishBytes(data)
	if err != nil {
		return fmt.Errorf("failed to publish task, error %v", err)
	}
	return nil
}

// StartServer starts listening for new request
func (fRuntime *FlowRuntime) StartServer() error {
	fRuntime.srv = &http.Server{
		Addr:           fmt.Sprintf(":%d", fRuntime.ServerPort),
		ReadTimeout:    fRuntime.ReadTimeout,
		WriteTimeout:   fRuntime.WriteTimeout,
		Handler:        Router(fRuntime),
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	return fRuntime.srv.ListenAndServe()
}

// StopServer stops the server
func (fRuntime *FlowRuntime) StopServer() error {
	if err := fRuntime.srv.Shutdown(context.Background()); err != nil {
		return err
	}
	return nil
}

// StartRuntime starts the runtime
func (fRuntime *FlowRuntime) StartRuntime() error {
	worker := &Worker{
		ID:          getNewId(),
		Concurrency: fRuntime.Concurrency,
	}

	registerDetails := func() error {
		// Get the flow details for each flow
		flowDetails := make(map[string]string)
		var err error
		fRuntime.Flows.ForEach(func(flowID string, defHandler FlowDefinitionHandler) bool {
			worker.mu.Lock()
			defer worker.mu.Unlock()

			var dag string
			worker.Flows = append(worker.Flows, flowID)
			dag, err = getFlowDefinition(defHandler)
			if err != nil {
				err = fmt.Errorf("failed to start runtime, dag export failed, error %v", err)
				return false
			}
			flowDetails[flowID] = dag
			return true
		})
		if err != nil {
			return err
		}

		if fRuntime.workerMode.Load() {
			err := fRuntime.saveWorkerDetails(worker)
			if err != nil {
				return fmt.Errorf("failed to register worker details, %v", err)
			}
		} else {
			err := fRuntime.deleteWorkerDetails(worker)
			if err != nil {
				return fmt.Errorf("failed to deregister worker details, %v", err)
			}
		}

		if err := fRuntime.saveFlowDetails(flowDetails); err != nil {
			return fmt.Errorf("failed to register flow details, %v", err)
		}

		return nil
	}

	err := registerDetails()
	if err != nil {
		log.Printf("failed to register details, %v", err)
		return err
	}

	err = gocron.Every(GoFlowRegisterInterval).Second().Do(func() {
		err := registerDetails()
		if err != nil {
			log.Printf("failed to register details, %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to start runtime, %v", err)
	}

	<-gocron.Start()

	return fmt.Errorf("[goflow] runtime stopped")
}

func (fRuntime *FlowRuntime) EnqueuePartialRequest(pr *runtime.Request) error {
	data, _ := json.Marshal(&Task{
		FlowName:    pr.FlowName,
		RequestID:   pr.RequestID,
		Body:        string(pr.Body),
		Header:      pr.Header,
		RawQuery:    pr.RawQuery,
		Query:       pr.Query,
		RequestType: PartialRequest,
	})
	err := fRuntime.taskQueues[pr.FlowName].PublishBytes(data)
	if err != nil {
		return fmt.Errorf("failed to publish task, error %v", err)
	}
	return nil
}

// Consume messages from queue
func (fRuntime *FlowRuntime) Consume(message rmq.Delivery) {
	var task Task
	if err := json.Unmarshal([]byte(message.Payload()), &task); err != nil {
		fRuntime.Logger.Log("[goflow] rejecting task for parse failure, error " + err.Error())
		if err := message.Push(); err != nil {
			fRuntime.Logger.Log("[goflow] failed to push message to retry queue, error " + err.Error())
			return
		}
		return
	}
	if err := fRuntime.handleRequest(makeRequestFromTask(task), task.RequestType); err != nil {
		fRuntime.Logger.Log("[goflow] rejecting task for failure, error " + err.Error())
		if err := message.Push(); err != nil {
			fRuntime.Logger.Log("[goflow] failed to push message to retry queue, error " + err.Error())
			return
		}
	}

	err := message.Ack()
	if err != nil {
		fRuntime.Logger.Log("[goflow] failed to acknowledge message, error " + err.Error())
		return
	}
}

func (fRuntime *FlowRuntime) handleRequest(request *runtime.Request, requestType string) error {
	var err error
	switch requestType {
	case PartialRequest:
		err = fRuntime.handlePartialRequest(request)
	case NewRequest:
		err = fRuntime.handleNewRequest(request)
	case PauseRequest:
		err = fRuntime.handlePauseRequest(request)
	case ResumeRequest:
		err = fRuntime.handleResumeRequest(request)
	case StopRequest:
		err = fRuntime.handleStopRequest(request)
	default:
		return fmt.Errorf("invalid request %v received with type %s", request, requestType)
	}
	return err
}

func (fRuntime *FlowRuntime) handleNewRequest(request *runtime.Request) error {
	flowExecutor, err := fRuntime.CreateExecutor(request)
	if err != nil {
		return fmt.Errorf("failed to execute request " + request.RequestID + ", error: " + err.Error())
	}

	response := &runtime.Response{}
	response.RequestID = request.RequestID
	response.Header = make(map[string][]string)

	err = controller.ExecuteFlowHandler(response, request, flowExecutor)
	if err != nil {
		return fmt.Errorf("request failed to be processed. error: " + err.Error())
	}

	return nil
}

func (fRuntime *FlowRuntime) handlePartialRequest(request *runtime.Request) error {
	flowExecutor, err := fRuntime.CreateExecutor(request)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to execute request, error: %v", request.RequestID, err))
		return fmt.Errorf("[goflow] failed to execute request " + request.RequestID + ", error: " + err.Error())
	}
	response := &runtime.Response{}
	response.RequestID = request.RequestID
	response.Header = make(map[string][]string)

	err = controller.PartialExecuteFlowHandler(response, request, flowExecutor)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be processed. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("[goflow] request failed to be processed. error: " + err.Error())
	}
	return nil
}

func (fRuntime *FlowRuntime) handlePauseRequest(request *runtime.Request) error {
	flowExecutor, err := fRuntime.CreateExecutor(request)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be paused. error: %v", request.RequestID, err))
		return fmt.Errorf("request %s failed to be paused. error: %v", request.RequestID, err.Error())
	}
	response := &runtime.Response{}
	response.RequestID = request.RequestID
	err = controller.PauseFlowHandler(response, request, flowExecutor)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be paused. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("request %s failed to be paused. error: %v", request.RequestID, err.Error())
	}
	return nil
}

func (fRuntime *FlowRuntime) handleResumeRequest(request *runtime.Request) error {
	flowExecutor, err := fRuntime.CreateExecutor(request)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be resumed. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("request %s failed to be resumed. error: %v", request.RequestID, err.Error())
	}
	response := &runtime.Response{}
	response.RequestID = request.RequestID
	err = controller.ResumeFlowHandler(response, request, flowExecutor)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be resumed. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("request %s failed to be resumed. error: %v", request.RequestID, err.Error())
	}
	return nil
}

func (fRuntime *FlowRuntime) handleStopRequest(request *runtime.Request) error {
	flowExecutor, err := fRuntime.CreateExecutor(request)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be stopped. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("request %s failed to be stopped. error: %v", request.RequestID, err.Error())
	}
	response := &runtime.Response{}
	response.RequestID = request.RequestID
	err = controller.StopFlowHandler(response, request, flowExecutor)
	if err != nil {
		fRuntime.Logger.Log(fmt.Sprintf("[request `%s`] failed to be stopped. error: %v", request.RequestID, err.Error()))
		return fmt.Errorf("request %s failed to be stopped. error: %v", request.RequestID, err.Error())
	}
	return nil
}

func (fRuntime *FlowRuntime) initializeTaskQueues(conn *rmq.Connection, flows *haxmap.Map[string, FlowDefinitionHandler]) error {

	if fRuntime.taskQueues == nil {
		fRuntime.taskQueues = make(map[string]rmq.Queue)
	}
	var outErr error
	flows.ForEach(func(flowName string, value FlowDefinitionHandler) bool {
		baseQId := fRuntime.internalRequestQueueId(flowName)
		taskQueue, err := (*conn).OpenQueue(baseQId)
		if err != nil {
			outErr = fmt.Errorf("failed to open queue, error %v", err)
			return false
		}

		var pushQueues = make([]rmq.Queue, fRuntime.RetryQueueCount)
		var prevQ = taskQueue

		for idx := 0; idx < fRuntime.RetryQueueCount; idx++ {
			pushQId := fmt.Sprintf("%s-push-%d", baseQId, idx)
			pushQueues[idx], err = (*conn).OpenQueue(pushQId)
			if err != nil {
				outErr = fmt.Errorf("failed to open push queue, error %v", err)
				return false
			}
			prevQ.SetPushQueue(pushQueues[idx])
			prevQ = pushQueues[idx]
		}

		err = taskQueue.StartConsuming(10, time.Second)
		if err != nil {
			outErr = fmt.Errorf("failed to start consumer taskQueue, error %v", err)
			return false
		}
		fRuntime.taskQueues[flowName] = taskQueue

		for idx := 0; idx < fRuntime.RetryQueueCount; idx++ {
			err = pushQueues[idx].StartConsuming(10, time.Second)
			if err != nil {
				outErr = fmt.Errorf("failed to start consumer pushQ1, error %v", err)
				return false
			}
		}

		for idx := 0; idx < fRuntime.Concurrency; idx++ {
			_, err := taskQueue.AddConsumer(fmt.Sprintf("request-consumer-%d", idx), fRuntime)
			if err != nil {
				outErr = fmt.Errorf("failed to add consumer, error %v", err)
				return false
			}
		}

		for idx := 0; idx < fRuntime.RetryQueueCount; idx++ {
			_, err = pushQueues[idx].AddConsumer(fmt.Sprintf("request-consumer-%d", idx), fRuntime)
			if err != nil {
				outErr = fmt.Errorf("failed to add consumer, error %v", err)
				return false
			}
		}
		return true
	})

	return outErr
}

func (fRuntime *FlowRuntime) cleanTaskQueues() error {

	if !reflect.ValueOf(fRuntime.rmqConnection).IsNil() {
		endChan := fRuntime.rmqConnection.StopAllConsuming()
		<-endChan
	}

	fRuntime.taskQueues = map[string]rmq.Queue{}

	return nil
}

func (fRuntime *FlowRuntime) internalRequestQueueId(flowName string) string {
	return fmt.Sprintf("%s:%s", InternalRequestQueueInitial, flowName)
}

func (fRuntime *FlowRuntime) requestQueueId(flowName string) string {
	return flowName
}

func (fRuntime *FlowRuntime) saveWorkerDetails(worker *Worker) error {
	rdb := fRuntime.rdb
	key := fmt.Sprintf("%s:%s", WorkerKeyInitial, worker.ID)
	value := marshalWorker(worker)
	rdb.Set(context.TODO(), key, value, time.Second*RDBKeyTimeOut)
	return nil
}

func (fRuntime *FlowRuntime) deleteWorkerDetails(worker *Worker) error {
	rdb := fRuntime.rdb
	key := fmt.Sprintf("%s:%s", WorkerKeyInitial, worker.ID)
	rdb.Del(context.TODO(), key)
	return nil
}

func (fRuntime *FlowRuntime) saveFlowDetails(flows map[string]string) error {
	rdb := fRuntime.rdb
	for flowId, definition := range flows {
		key := fmt.Sprintf("%s:%s", FlowKeyInitial, flowId)
		rdb.Set(context.TODO(), key, definition, time.Second*RDBKeyTimeOut)
	}
	return nil
}

func marshalWorker(worker *Worker) string {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	jsonDef, _ := json.Marshal(worker)
	return string(jsonDef)
}

func makeRequestFromTask(task Task) *runtime.Request {
	request := &runtime.Request{
		FlowName:  task.FlowName,
		RequestID: task.RequestID,
		Body:      []byte(task.Body),
		Header:    task.Header,
		RawQuery:  task.RawQuery,
		Query:     task.Query,
	}
	return request
}

func getFlowDefinition(handler FlowDefinitionHandler) (string, error) {
	ex := &FlowExecutor{
		Handler: handler,
	}
	flowExporter := exporter.CreateFlowExporter(ex)
	resp, err := flowExporter.Export()
	if err != nil {
		return "", err
	}
	return string(resp), nil
}

func getNewId() string {
	guid := xid.New()
	return guid.String()
}
