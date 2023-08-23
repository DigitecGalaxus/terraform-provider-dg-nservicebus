package endpoint

import (
	"context"
	"fmt"
	"strings"
	"terraform-provider-dg-servicebus/internal/provider/asb"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/exp/slices"
)

func (r *endpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state endpointResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model := state.ToAsbModel()

	state.QueueExists = types.BoolValue(true)
	state.EndpointExists = types.BoolValue(true)
	state.ShouldCreateQueue = types.BoolValue(false)
	state.ShouldCreateEndpoint = types.BoolValue(false)
	state.ShouldUpdateSubscriptions = types.BoolValue(false)
	state.HasMalformedFilters = types.BoolValue(false)

	var success bool

	success = r.updateEndpointQueueState(ctx, model, &state, resp)
	if !success {
		return
	}

	hasSubscribers := len(model.Subscriptions) > 0

	if hasSubscribers {
		endpointExists, success := r.updateEndpointState(ctx, model, &state, resp)
		if !success {
			return
		}

		// There are no subscriptions to check if the endpoint does not exist
		if endpointExists {
			success = r.updateEndpointSubscriptionState(ctx, model, &state)
			if !success {
				return
			}
		}
	}

	success = r.updateAdditionalQueueState(ctx, &state, resp)
	if !success {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *endpointResource) updateEndpointQueueState(ctx context.Context, model asb.EndpointModel, state *endpointResourceModel, resp *resource.ReadResponse) bool {
	queue, err := r.client.GetEndpointQueue(ctx, model)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading Queue",
			"Could not get Queue, unexpected error: "+err.Error(),
		)
		return false
	}

	if queue == nil {
		state.QueueExists = types.BoolValue(false)
		return true
	}

	maxQueueSizeInMb := *queue.QueueProperties.MaxSizeInMegabytes
	partitioningIsEnabled := *queue.QueueProperties.EnablePartitioning
	if partitioningIsEnabled {
		maxQueueSizeInMb = maxQueueSizeInMb / 16
	}

	state.QueueOptions.MaxSizeInMegabytes = types.Int64Value(int64(maxQueueSizeInMb))
	state.QueueOptions.EnablePartitioning = types.BoolValue(partitioningIsEnabled)

	return true
}

func (r *endpointResource) updateEndpointState(ctx context.Context, model asb.EndpointModel, state *endpointResourceModel, resp *resource.ReadResponse) ( /* Endpoint Exists */ bool /* Success */, bool) {
	endpointExists, err := r.client.EndpointExists(ctx, model)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading Endpoint",
			"Could not read if an Endpoint exists, unexpected error: "+err.Error(),
		)

		return false, false
	}

	if !endpointExists {
		state.EndpointExists = types.BoolValue(false)
		state.Subscriptions = []string{}
	}

	return endpointExists, true
}

func (r *endpointResource) updateEndpointSubscriptionState(
	ctx context.Context,
	loadedState asb.EndpointModel,
	currentState *endpointResourceModel,
) bool {
	// Azure subscription names are cut to a length of 50 characters
	getFullSubscriptionNameBySuffixInState := func(subscriptionSuffix string) *string {
		for _, subscription := range loadedState.Subscriptions {
			if strings.HasSuffix(subscription, subscriptionSuffix) {
				return &subscription
			}
		}

		return nil
	}

	azureSubscriptions, err := r.client.GetEndpointSubscriptions(ctx, loadedState)
	if err != nil {
		return false
	}

	updatedSubscriptionState := []string{}
	for _, azureSubscription := range azureSubscriptions {
		subscriptionName := getFullSubscriptionNameBySuffixInState(azureSubscription.Name)
		if subscriptionName == nil {
			// Add to the state, which will delete the resource on apply
			updatedSubscriptionState = append(updatedSubscriptionState, azureSubscription.Name)
			continue
		}

		if !asb.IsFilterCorrect(azureSubscription.Filter, *subscriptionName) {
			currentState.HasMalformedFilters = types.BoolValue(true)
		}

		updatedSubscriptionState = append(updatedSubscriptionState, *subscriptionName)
	}

	currentState.Subscriptions = updatedSubscriptionState
	return true
}

func (r *endpointResource) updateAdditionalQueueState(ctx context.Context, state *endpointResourceModel, resp *resource.ReadResponse) bool {
	for _, queue := range state.AdditionalQueues {
		queueExists, err := r.client.QueueExists(ctx, queue)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error reading queue",
				fmt.Sprintf("Could not read if additional queue %s exists, unexpected error: %q", queue, err.Error()),
			)
			return false
		}

		if !queueExists {
			index := slices.Index(state.AdditionalQueues, queue)
			slices.Delete(state.AdditionalQueues, index, index+1)
		}
	}
	return true
}
