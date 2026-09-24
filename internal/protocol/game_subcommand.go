package protocol

import "fmt"

const (
	SubCommandFinish     = 4
	SubCommandChat       = 31
	SubCommandUnitAdd    = 32
	SubCommandUnitRemove = 33
	SubCommandUnitMove   = 35
)

// SubCommand defines the interface for a game subcommand.
type SubCommand interface {
	subCommand()
}

type SubCommandChatPacket struct {
	Message string
}

func (p SubCommandChatPacket) subCommand() {}

// ParseSubCommands parses the data from a GameCommandPacket into a slice of SubCommands.
func ParseSubCommands(data []byte) ([]SubCommand, error) {
	r := NewReader(data)
	var commands []SubCommand

	for r.Remaining() > 0 {
		cmdType, err := r.Byte()
		if err != nil {
			return nil, fmt.Errorf("read subcommand type: %w", err)
		}

		var cmd SubCommand
		switch cmdType {
		case SubCommandChat:
			message, err := r.String()
			if err != nil {
				return nil, fmt.Errorf("decode chat subcommand: %w", err)
			}
			cmd = SubCommandChatPacket{Message: message}
		default:
			// For unknown commands, we can't reliably continue parsing.
			// It's safer to stop here.
			return commands, fmt.Errorf("unknown subcommand type: %d", cmdType)
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}
