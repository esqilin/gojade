package jade

import (
    "github.com/xthexder/go-jack"
)

type audioInPort struct {
    jackPort *jack.Port
    ch chan<- float64
}

type audioOutPort struct {
    jackPort *jack.Port
    ch <-chan float64
}

type midiInPort struct {
    jackPort *jack.Port
    ch chan<- jack.MidiData
}

type midiOutPort struct {
    jackPort *jack.Port
    ch <-chan jack.MidiData
}
