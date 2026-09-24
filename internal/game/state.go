package game

import "sync"

// UnitID uniquely identifies a unit in the game world.
type UnitID int64

// Unit represents a single entity in the game world.
type Unit struct {
	ID    UnitID
	Type  string
	X, Y  float32
	Owner string // PlayerID
}

// State represents the entire state of a game room.
type State struct {
	mu         sync.RWMutex
	units      map[UnitID]*Unit
	nextUnitID UnitID
	dirtyUnits []*Unit
}

// NewState creates a new game state.
func NewState() *State {
	return &State{
		units:      make(map[UnitID]*Unit),
		nextUnitID: 1,
	}
}

// AddUnit creates a new unit, assigns it a unique ID, and adds it to the game state.
func (s *State) AddUnit(unitType, owner string, x, y float32) *Unit {
	s.mu.Lock()
	defer s.mu.Unlock()

	newUnit := &Unit{
		ID:    s.nextUnitID,
		Type:  unitType,
		Owner: owner,
		X:     x,
		Y:     y,
	}
	s.units[s.nextUnitID] = newUnit
	s.dirtyUnits = append(s.dirtyUnits, newUnit)
	s.nextUnitID++

	return newUnit
}

// CollectAndClearDirty returns the list of units that have changed since the last call
// and clears the internal list.
func (s *State) CollectAndClearDirty() []*Unit {
	s.mu.Lock()
	defer s.mu.Unlock()

	dirty := s.dirtyUnits
	s.dirtyUnits = nil
	return dirty
}
