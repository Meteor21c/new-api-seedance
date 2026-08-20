package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTokenAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestSetupContextForTokenPreservesCustomAutoGroupsOrder(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `["vip","default"]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{"vip", "default"}, value)
}

func TestSetupContextForTokenTreatsStoredEmptyArrayAsInheritance(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `[]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	assert.False(t, ok)
}

func TestSetupContextForTokenMalformedAutoGroupsFailsClosed(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `not-json`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{}, value)
}

func TestSetupContextForSmartTextTokenAllowsResponsesAndSetsContext(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	token := &model.Token{Id: 1, UserId: 2, SmartText: true, Group: "auto"}

	require.NoError(t, SetupContextForToken(ctx, token))
	assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenSmartText))
}

func TestSetupContextForSmartTextTokenRejectsMediaEndpoint(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	token := &model.Token{Id: 1, UserId: 2, SmartText: true, Group: "auto"}

	err := SetupContextForToken(ctx, token)

	require.Error(t, err)
	assert.True(t, ctx.IsAborted())
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}
