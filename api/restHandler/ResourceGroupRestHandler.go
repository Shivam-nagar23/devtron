/*
 * Copyright (c) 2020-2024. Devtron Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package restHandler

import (
	"encoding/json"
	"fmt"
	"github.com/devtron-labs/devtron/api/restHandler/common"
	"github.com/devtron-labs/devtron/pkg/auth/authorisation/casbin"
	"github.com/devtron-labs/devtron/pkg/auth/user"
	"github.com/devtron-labs/devtron/pkg/resourceGroup"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"gopkg.in/go-playground/validator.v9"
	"net/http"
	"strconv"
)

type ResourceGroupRestHandler interface {
	GetActiveResourceGroupList(w http.ResponseWriter, r *http.Request)
	//GetApplicationsForResourceGroup(w http.ResponseWriter, r *http.Request)
	CreateResourceGroup(w http.ResponseWriter, r *http.Request)
	UpdateResourceGroup(w http.ResponseWriter, r *http.Request)
	DeleteResourceGroup(w http.ResponseWriter, r *http.Request)
	CheckResourceGroupPermissions(w http.ResponseWriter, r *http.Request)
}

type ResourceGroupRestHandlerImpl struct {
	logger               *zap.SugaredLogger
	enforcer             casbin.Enforcer
	userService          user.UserService
	resourceGroupService resourceGroup.ResourceGroupService
	validator            *validator.Validate
}

func NewResourceGroupRestHandlerImpl(logger *zap.SugaredLogger, enforcer casbin.Enforcer,
	userService user.UserService, resourceGroupService resourceGroup.ResourceGroupService,
	validator *validator.Validate) *ResourceGroupRestHandlerImpl {
	userAuthHandler := &ResourceGroupRestHandlerImpl{
		logger:               logger,
		enforcer:             enforcer,
		userService:          userService,
		resourceGroupService: resourceGroupService,
		validator:            validator,
	}
	return userAuthHandler
}

func (handler ResourceGroupRestHandlerImpl) getGroupTypeAndAuthFunc(groupType string) (resourceGroup.ResourceGroupType, func(token string, appObject []string, action string) map[string]bool, error) {
	var resourceGroupType resourceGroup.ResourceGroupType
	var authFunc func(token string, appObject []string, action string) map[string]bool
	if groupType == "env-group" {
		resourceGroupType = resourceGroup.ENV_GROUP
		authFunc = handler.checkEnvAuthBatch
	} else if groupType == "" || groupType == "app-group" {
		//maintains backward compatibility for app groups
		resourceGroupType = resourceGroup.APP_GROUP
		authFunc = handler.checkAppAuthBatch
	} else {
		return "", nil, fmt.Errorf("invalid group type %s", groupType)
	}
	return resourceGroupType, authFunc, nil
}

func (handler ResourceGroupRestHandlerImpl) GetActiveResourceGroupList(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")

	vars := mux.Vars(r)
	resourceId, err := strconv.Atoi(vars["resourceId"])
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	groupType, authFunc, err := handler.getGroupTypeAndAuthFunc(vars["groupType"])
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	res, err := handler.resourceGroupService.GetActiveResourceGroupList(token, authFunc, resourceId, groupType)
	if err != nil {
		handler.logger.Errorw("service err, GetActiveResourceGroupList", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	common.WriteJsonResp(w, nil, res, http.StatusOK)
}

//	func (handler ResourceGroupRestHandlerImpl) GetApplicationsForResourceGroup(w http.ResponseWriter, r *http.Request) {
//		userId, err := handler.userService.GetLoggedInUser(r)
//		if userId == 0 || err != nil {
//			common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
//			return
//		}
//		token := r.Header.Get("token")
//		if ok := handler.enforcer.Enforce(token, casbin.ResourceGlobal, casbin.ActionGet, "*"); !ok {
//			common.WriteJsonResp(w, errors.New("unauthorized"), nil, http.StatusForbidden)
//			return
//		}
//		vars := mux.Vars(r)
//		id, err := strconv.Atoi(vars["appGroupId"])
//		if err != nil {
//			common.WriteJsonResp(w, err, "", http.StatusBadRequest)
//			return
//		}
//		//res, err := handler.resourceGroupService.GetApplicationsForResourceGroup(id)
//		if err != nil {
//			handler.logger.Errorw("service err, GetApplicationsForResourceGroup", "err", err)
//			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
//			return
//		}
//		common.WriteJsonResp(w, nil, res, http.StatusOK)
//	}
func (handler ResourceGroupRestHandlerImpl) CreateResourceGroup(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")
	userId, err := handler.userService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	decoder := json.NewDecoder(r.Body)
	var request resourceGroup.ResourceGroupDto
	err = decoder.Decode(&request)
	if err != nil {
		handler.logger.Errorw("request err, CreateResourceGroup", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.UserId = userId
	err = handler.validator.Struct(request)
	if err != nil {
		handler.logger.Errorw("validation error", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	resourceId, err := strconv.Atoi(vars["resourceId"])
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	groupType, authFunc, err := handler.getGroupTypeAndAuthFunc(string(request.GroupType))
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	request.ParentResourceId = resourceId
	request.GroupType = groupType
	//To maintain backward compatibility
	if groupType == resourceGroup.APP_GROUP {
		if request.EnvironmentId > 0 {
			request.ParentResourceId = request.EnvironmentId
		}
		if len(request.AppIds) > 0 {
			request.ResourceIds = request.AppIds
		}
	}

	err = handler.validator.Struct(request)
	if err != nil {
		handler.logger.Errorw("validation error", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	handler.logger.Infow("request payload, CreateResourceGroup", "payload", request)
	request.CheckAuthBatch = authFunc
	resp, err := handler.resourceGroupService.CreateResourceGroup(&request, token)
	if err != nil {
		handler.logger.Errorw("service err, CreateResourceGroup", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	common.WriteJsonResp(w, nil, resp, http.StatusOK)
}
func (handler ResourceGroupRestHandlerImpl) UpdateResourceGroup(w http.ResponseWriter, r *http.Request) {

	token := r.Header.Get("token")
	userId, err := handler.userService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	decoder := json.NewDecoder(r.Body)
	var request resourceGroup.ResourceGroupDto
	err = decoder.Decode(&request)
	if err != nil {
		handler.logger.Errorw("request err, UpdateResourceGroup", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.UserId = userId

	groupType, authFunc, err := handler.getGroupTypeAndAuthFunc(string(request.GroupType))
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.GroupType = groupType
	//To maintain backward compatibility
	if groupType == resourceGroup.APP_GROUP {
		if request.EnvironmentId > 0 {
			request.ParentResourceId = request.EnvironmentId
		}

		if len(request.AppIds) > 0 {
			request.ResourceIds = request.AppIds
		}
	}

	err = handler.validator.Struct(request)
	if err != nil {
		handler.logger.Errorw("validation error", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	handler.logger.Infow("request payload, UpdateResourceGroup", "payload", request)
	request.CheckAuthBatch = authFunc
	resp, err := handler.resourceGroupService.UpdateResourceGroup(&request, token)
	if err != nil {
		handler.logger.Errorw("service err, UpdateResourceGroup", "err", err, "payload", request)
		common.WriteJsonResp(w, err, resp, http.StatusInternalServerError)
		return
	}
	common.WriteJsonResp(w, nil, resp, http.StatusOK)
}
func (handler ResourceGroupRestHandlerImpl) DeleteResourceGroup(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")

	vars := mux.Vars(r)
	resourceGroupId, err := strconv.Atoi(vars["resourceGroupId"])
	if err != nil {
		common.WriteJsonResp(w, err, "", http.StatusBadRequest)
		return
	}

	groupType, authFunc, err := handler.getGroupTypeAndAuthFunc(vars["groupType"])

	handler.logger.Infow("request payload, DeleteResourceGroup", "resourceGroupId", resourceGroupId)
	resp, err := handler.resourceGroupService.DeleteResourceGroup(resourceGroupId, groupType, token, authFunc)
	if err != nil {
		handler.logger.Errorw("service err, DeleteResourceGroup", "err", err, "resourceGroupId", resourceGroupId, "groupType", groupType)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	common.WriteJsonResp(w, nil, resp, http.StatusOK)
}
func (handler ResourceGroupRestHandlerImpl) CheckResourceGroupPermissions(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")

	userId, err := handler.userService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	decoder := json.NewDecoder(r.Body)
	var request resourceGroup.ResourceGroupDto
	err = decoder.Decode(&request)
	if err != nil {
		handler.logger.Errorw("request err, CreateResourceGroup", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.UserId = userId
	vars := mux.Vars(r)
	resourceId, err := strconv.Atoi(vars["resourceId"])
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	groupType, authFunc, err := handler.getGroupTypeAndAuthFunc(string(request.GroupType))
	if err != nil {
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.GroupType = groupType
	request.ParentResourceId = resourceId
	//To maintain backward compatibility
	if groupType == resourceGroup.APP_GROUP {
		if request.EnvironmentId > 0 {
			request.ParentResourceId = request.EnvironmentId
		}

		if len(request.AppIds) > 0 {
			request.ResourceIds = request.AppIds
		}
	}

	handler.logger.Infow("request payload, CheckResourceGroupPermissions", "payload", request)
	request.CheckAuthBatch = authFunc
	resp, err := handler.resourceGroupService.CheckResourceGroupPermissions(&request, token)
	if err != nil {
		handler.logger.Errorw("service err", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	common.WriteJsonResp(w, nil, resp, http.StatusOK)
}

func (handler ResourceGroupRestHandlerImpl) checkAppAuthBatch(token string, appObject []string, action string) map[string]bool {
	var appResult map[string]bool
	if len(appObject) > 0 {
		appResult = handler.enforcer.EnforceInBatch(token, casbin.ResourceApplications, action, appObject)
	}
	return appResult
}

func (handler ResourceGroupRestHandlerImpl) checkEnvAuthBatch(token string, envObject []string, action string) map[string]bool {
	var appResult map[string]bool
	if len(envObject) > 0 {
		appResult = handler.enforcer.EnforceInBatch(token, casbin.ResourceEnvironment, action, envObject)
	}
	return appResult
}
