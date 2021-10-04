package collectors

import (
	"fmt"
	lp "github.com/influxdata/line-protocol"
	"io/ioutil"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const LIDFILE = `/sys/class/infiniband/mlx4_0/ports/1/lid`

type InfinibandCollector struct {
	MetricCollector
	tags map[string]string
}

func (m *InfinibandCollector) Init() error {
	m.name = "InfinibandCollector"
	m.setup()
	m.tags = map[string]string{"type": "node"}
	_, err := ioutil.ReadFile(string(LIDFILE))
	if err == nil {
		m.init = true
	}
	return err
}

func (m *InfinibandCollector) Read(interval time.Duration, out *[]lp.MutableMetric) {
	buffer, err := ioutil.ReadFile(string(LIDFILE))

	if err != nil {
		log.Print(err)
		return
	}

	args := fmt.Sprintf("-r %s 1 0xf000", string(buffer))

	command := exec.Command("/usr/sbin/perfquery", args)
	command.Wait()
	stdout, err := command.Output()
	if err != nil {
		log.Print(err)
		return
	}

	ll := strings.Split(string(stdout), "\n")

	for _, line := range ll {
		if strings.HasPrefix(line, "PortRcvData") || strings.HasPrefix(line, "RcvData") {
			lv := strings.Fields(line)
			v, err := strconv.ParseFloat(lv[1], 64)
			if err == nil {
				y, err := lp.New("ib_recv", m.tags, map[string]interface{}{"value": float64(v)}, time.Now())
				if err == nil {
					*out = append(*out, y)
				}
			}
		}
		if strings.HasPrefix(line, "PortXmitData") || strings.HasPrefix(line, "XmtData") {
			lv := strings.Fields(line)
			v, err := strconv.ParseFloat(lv[1], 64)
			if err == nil {
				y, err := lp.New("ib_xmit", m.tags, map[string]interface{}{"value": float64(v)}, time.Now())
				if err == nil {
					*out = append(*out, y)
				}
			}
		}
	}
}

func (m *InfinibandCollector) Close() {
	m.init = false
	return
}
