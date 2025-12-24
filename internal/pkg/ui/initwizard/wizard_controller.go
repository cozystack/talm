package initwizard

import (
	"fmt"
	"log"
)

type WizardController struct {
    state WizardState
    data  *InitData
}

// NewWizardController создает новый контроллер мастера
func NewWizardController(data *InitData) *WizardController {
    return &WizardController{
        state: StatePreset,
        data:  data,
    }
}

func (c *WizardController) Transition(to WizardState) error {
	log.Printf("[DEBUG-CONTROLLER] Попытка перехода из %s в %s", c.state, to)
	if !isAllowed(c.state, to) {
		log.Printf("[DEBUG-CONTROLLER] ПЕРЕХОД ЗАПРЕЩЕН: %s -> %s", c.state, to)
		return fmt.Errorf(
			"invalid transition %s -> %s",
			c.state,
			to,
		)
	}

	c.state = to
	log.Printf("[DEBUG-CONTROLLER] Переход выполнен, новое состояние: %s", c.state)
	return nil
}
