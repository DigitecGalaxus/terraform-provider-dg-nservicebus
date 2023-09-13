package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"terraform-provider-dg-servicebus/internal/provider/asb"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

func (r *endpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan endpointResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model := plan.ToAsbModel()

	if plan.ShouldCreateQueue.ValueBool() {
		if !r.createEndpointQueue(ctx, model, resp) {
			return
		}
	} else {
		tflog.Info(ctx, "Queue already exists, skipping creation")
	}

	if plan.ShouldCreateEndpoint.ValueBool() {
		if !r.createAdditionalQueues(ctx, model, resp) {
			return
		}
	}	else {
		tflog.Info(ctx, "Endpoint already exists, skipping creation")
	}

	err := r.client.CreateEndpointWithDefaultRule(ctx, model)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating subscription",
			"Could not create subscription, unexpected error: "+err.Error(),
		)
		return
	}

	for i := 0; i < len(plan.Subscriptions); i++ {
		err := r.client.CreateEndpointSubscription(ctx, model, plan.Subscriptions[i])
		if err != nil {
			resp.Diagnostics.AddError(
				"Error creating rule",
				"Could not create rule, unexpected error: "+err.Error(),
			)
			return
		}
	}

	plan.QueueExists = types.BoolValue(true)
	plan.EndpointExists = types.BoolValue(true)
	plan.ShouldCreateQueue = types.BoolValue(false)
	plan.ShouldCreateEndpoint = types.BoolValue(false)
	plan.HasMalformedFilters = types.BoolValue(false)
	plan.ShouldUpdateSubscriptions = types.BoolValue(false)

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *endpointResource) createEndpointQueue(ctx context.Context, model asb.EndpointModel, resp *resource.CreateResponse) bool {
	err := r.client.CreateEndpointQueue(ctx, model.EndpointName, model.QueueOptions)
	if err == nil {
		return true
	}

	var respError *azcore.ResponseError
	switch {
	case errors.As(err, &respError):
		if respError.StatusCode == http.StatusConflict {
			resp.Diagnostics.AddError(
				"Resource already exists",
				"This resource already exists and is tracked outside of Terraform. "+
					"To track this resource you have to import it into state with: "+
					"'terraform import dgservicebus_endpoint.<Block label> <topic_name>,<endpoint_name>'",
			)
			return false
		}

		resp.Diagnostics.AddError(
			"Error creating queue",
			"Could not create queue, unexpected error: "+err.Error(),
		)
	default:
		resp.Diagnostics.AddError(
			"Error creating queue",
			"Could not create queue, unexpected error: "+err.Error(),
		)

		return false
	}
	
	return false
}

func (r *endpointResource) createAdditionalQueues(ctx context.Context, model asb.EndpointModel, resp *resource.CreateResponse) bool {
	for _, queue := range model.AdditionalQueues {
		err := r.client.CreateEndpointQueue(ctx, queue, model.QueueOptions)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error creating additional queue",
				fmt.Sprintf("Could not create queue %s, unexpected error: %q", queue, err.Error()),
			)
			return false
		}
	}
	return true
}
