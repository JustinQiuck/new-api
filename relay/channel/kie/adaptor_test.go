package kie

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesRequestUsesKieRouteAndModel(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeKie,
			ChannelBaseUrl:    "https://api.kie.ai",
			UpstreamModelName: PublicTextModel,
		},
		RelayMode: relayconstant.RelayModeResponses,
	}

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.kie.ai/codex/v1/responses", requestURL)

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{Model: PublicTextModel})
	require.NoError(t, err)
	convertedRequest, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, KieTextModel, convertedRequest.Model)
	assert.Equal(t, KieTextModel, info.UpstreamModelName)
}

func TestResponsesRequestPreservesExplicitChannelModelMapping(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "custom-kie-model"},
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{Model: PublicTextModel})
	require.NoError(t, err)
	convertedRequest, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "custom-kie-model", convertedRequest.Model)
}

func TestSetupRequestHeaderUsesBearerKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	header := http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "kie-secret"}}

	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer kie-secret", header.Get("Authorization"))
}

func TestUnsupportedRelayModeIsRejectedBeforeUpstream(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.kie.ai"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}

	_, err := (&Adaptor{}).GetRequestURL(info)
	require.ErrorContains(t, err, "does not support relay mode")
}
