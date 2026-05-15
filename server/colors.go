package main

// SlackAttachment color tokens shared across per-verb renderers. The hex
// values are the contract published in the spike design comment §0.2
// (mattermost-plugin-fulcrum#6); any change here is a cross-feature visual
// change and warrants its own PR.
const (
	colorAccent          = "#6F42C1"
	colorStatusTODO      = "#95A5A6"
	colorStatusDoing     = "#3498DB"
	colorStatusReview    = "#9B59B6"
	colorStatusDone      = "#2ECC71"
	colorStatusCanceled  = "#7F8C8D"
	colorPriorityHigh    = "#E74C3C"
	colorPriorityMedium  = "#F39C12"
	colorPriorityLow     = "#BDC3C7"
	colorWarn            = "#D97706"
	colorError           = "#B91C1C"
)
