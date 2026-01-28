package initwizard

var allowedTransitions = map[WizardState][]WizardState{
    StatePreset: {
        StateEndpoint,
        StateAddNodeScan,
    },
    StateEndpoint: {
        StateScanning,
        StateNodeSelect,
    },
    StateScanning: {
        StateNodeSelect,
        StateEndpoint, // cancel
    },
    StateNodeSelect: {
        StateNodeConfig,
        StateEndpoint,
    },
    StateNodeConfig: {
        StateConfirm,
        StateNodeSelect,
    },
    StateConfirm: {
        StateGenerate,
        StateNodeConfig,
    },
    StateGenerate: {
        StateDone,
    },
    StateAddNodeScan: {
        StateScanning,
        StateNodeSelect,
    },
    StateCozystackScan: {
        StateNodeSelect,
    },
    StateVIPConfig: {
        StateNetworkConfig,
    },
    StateNetworkConfig: {
        StateNodeDetails,
    },
    StateNodeDetails: {
        StateConfirm,
    },
}

func isAllowed(from, to WizardState) bool {
    for _, s := range allowedTransitions[from] {
        if s == to {
            return true
        }
    }
    return false
}
