package initwizard

import (
	"fmt"
	"log"
)

type WizardController struct {
    state WizardState
    data  *InitData
}

// NewWizardController creates a new wizard controller
func NewWizardController(data *InitData) *WizardController {
    return &WizardController{
        state: StatePreset,
        data:  data,
    }
}

func (c *WizardController) Transition(to WizardState) error {
	log.Printf("[DEBUG-CONTROLLER] Attempting transition from %s to %s", c.state, to)
	if !isAllowed(c.state, to) {
		log.Printf("[DEBUG-CONTROLLER] TRANSITION FORBIDDEN: %s -> %s", c.state, to)
		return fmt.Errorf(
			"invalid transition %s -> %s",
			c.state,
			to,
		)
	}

	c.state = to
	log.Printf("[DEBUG-CONTROLLER] Transition completed, new state: %s", c.state)
	return nil
}
