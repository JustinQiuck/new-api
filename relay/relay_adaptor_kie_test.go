package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	taskkie "github.com/QuantumNous/new-api/relay/channel/task/kie"
	"github.com/stretchr/testify/require"
)

func TestKieTaskAdaptorRegistration(t *testing.T) {
	adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKie)))
	require.IsType(t, &taskkie.TaskAdaptor{}, adaptor)
}
