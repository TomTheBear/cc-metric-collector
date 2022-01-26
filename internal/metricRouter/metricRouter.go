package metricRouter

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	cclog "github.com/ClusterCockpit/cc-metric-collector/internal/ccLogger"

	lp "github.com/ClusterCockpit/cc-metric-collector/internal/ccMetric"
	mct "github.com/ClusterCockpit/cc-metric-collector/internal/multiChanTicker"
	"gopkg.in/Knetic/govaluate.v2"
)

// Metric router tag configuration
type metricRouterTagConfig struct {
	Key       string `json:"key"`   // Tag name
	Value     string `json:"value"` // Tag value
	Condition string `json:"if"`    // Condition for adding or removing corresponding tag
}

// Metric router configuration
type metricRouterConfig struct {
	AddTags       []metricRouterTagConfig `json:"add_tags"`           // List of tags that are added when the condition is met
	DelTags       []metricRouterTagConfig `json:"delete_tags"`        // List of tags that are removed when the condition is met
	IntervalStamp bool                    `json:"interval_timestamp"` // Update timestamp periodically?
}

type metricRouter struct {
	inputs    []chan lp.CCMetric // List of all input channels
	outputs   []chan lp.CCMetric // List of all output channels
	done      chan bool          // channel to finish / stop metric router
	wg        *sync.WaitGroup
	timestamp time.Time // timestamp
	ticker    mct.MultiChanTicker
	config    metricRouterConfig
}

// MetricRouter access functions
type MetricRouter interface {
	Init(ticker mct.MultiChanTicker, wg *sync.WaitGroup, routerConfigFile string) error
	AddInput(input chan lp.CCMetric)
	AddOutput(output chan lp.CCMetric)
	Start()
	Close()
}

// Init initializes a metric router by setting up:
// * input and output channels
// * done channel
// * wait group synchronization (from variable wg)
// * ticker (from variable ticker)
// * configuration (read from config file in variable routerConfigFile)
func (r *metricRouter) Init(ticker mct.MultiChanTicker, wg *sync.WaitGroup, routerConfigFile string) error {
	r.inputs = make([]chan lp.CCMetric, 0)
	r.outputs = make([]chan lp.CCMetric, 0)
	r.done = make(chan bool)
	r.wg = wg
	r.ticker = ticker
	configFile, err := os.Open(routerConfigFile)
	if err != nil {
		cclog.ComponentError("MetricRouter", err.Error())
		return err
	}
	defer configFile.Close()
	jsonParser := json.NewDecoder(configFile)
	err = jsonParser.Decode(&r.config)
	if err != nil {
		cclog.ComponentError("MetricRouter", err.Error())
		return err
	}
	return nil
}

// StartTimer starts a timer which updates timestamp periodically
func (r *metricRouter) StartTimer() {
	m := make(chan time.Time)
	r.ticker.AddChannel(m)
	go func() {
		for {
			t := <-m
			r.timestamp = t
		}
	}()
}

// EvalCondition evaluates condition Cond for metric data from point
func (r *metricRouter) EvalCondition(Cond string, point lp.CCMetric) (bool, error) {
	expression, err := govaluate.NewEvaluableExpression(Cond)
	if err != nil {
		cclog.ComponentDebug("MetricRouter", Cond, " = ", err.Error())
		return false, err
	}

	// Add metric name, tags, meta data, fields and timestamp to the parameter list
	params := make(map[string]interface{})
	params["name"] = point.Name()
	for _, t := range point.TagList() {
		params[t.Key] = t.Value
	}
	for _, m := range point.MetaList() {
		params[m.Key] = m.Value
	}
	for _, f := range point.FieldList() {
		params[f.Key] = f.Value
	}
	params["timestamp"] = point.Time()

	// evaluate condition
	result, err := expression.Evaluate(params)
	if err != nil {
		cclog.ComponentDebug("MetricRouter", Cond, " = ", err.Error())
		return false, err
	}
	return bool(result.(bool)), err
}

// DoAddTags adds a tag when condition is fullfiled
func (r *metricRouter) DoAddTags(point lp.CCMetric) {
	for _, m := range r.config.AddTags {
		var conditionMatches bool

		if m.Condition == "*" {
			conditionMatches = true
		} else {
			var err error
			conditionMatches, err = r.EvalCondition(m.Condition, point)
			if err != nil {
				cclog.ComponentError("MetricRouter", err.Error())
				conditionMatches = false
			}
		}
		if conditionMatches {
			point.AddTag(m.Key, m.Value)
		}
	}
}

// DoDelTags removes a tag when condition is fullfiled
func (r *metricRouter) DoDelTags(point lp.CCMetric) {
	for _, m := range r.config.DelTags {
		var conditionMatches bool

		if m.Condition == "*" {
			conditionMatches = true
		} else {
			var err error
			conditionMatches, err = r.EvalCondition(m.Condition, point)
			if err != nil {
				cclog.ComponentError("MetricRouter", err.Error())
				conditionMatches = false
			}
		}
		if conditionMatches {
			point.RemoveTag(m.Key)
		}
	}
}

// Start starts the metric router
func (r *metricRouter) Start() {
	r.wg.Add(1)
	r.timestamp = time.Now()
	if r.config.IntervalStamp {
		r.StartTimer()
	}
	go func() {
		for {
		RouterLoop:
			select {
			case <-r.done:
				cclog.ComponentDebug("MetricRouter", "DONE")
				r.wg.Done()
				break RouterLoop
			default:
				for _, c := range r.inputs {
				RouterInputLoop:
					select {
					case <-r.done:
						cclog.ComponentDebug("MetricRouter", "DONE")
						r.wg.Done()
						break RouterInputLoop
					case p := <-c:
						cclog.ComponentDebug("MetricRouter", "FORWARD", p)
						r.DoAddTags(p)
						r.DoDelTags(p)
						if r.config.IntervalStamp {
							p.SetTime(r.timestamp)
						}
						for _, o := range r.outputs {
							o <- p
						}
					default:
					}
				}
			}
		}
	}()
	cclog.ComponentDebug("MetricRouter", "STARTED")
}

// AddInput adds a input channel to the metric router
func (r *metricRouter) AddInput(input chan lp.CCMetric) {
	r.inputs = append(r.inputs, input)
}

// AddOutput adds a output channel to the metric router
func (r *metricRouter) AddOutput(output chan lp.CCMetric) {
	r.outputs = append(r.outputs, output)
}

// Close finishes / stops the metric router
func (r *metricRouter) Close() {
	r.done <- true
	cclog.ComponentDebug("MetricRouter", "CLOSE")
}

// New creates a new initialized metric router
func New(ticker mct.MultiChanTicker, wg *sync.WaitGroup, routerConfigFile string) (MetricRouter, error) {
	r := new(metricRouter)
	err := r.Init(ticker, wg, routerConfigFile)
	if err != nil {
		return nil, err
	}
	return r, err
}
