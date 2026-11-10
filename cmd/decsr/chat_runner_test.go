package main

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

func TestChatRunner_UsesPMConfig(t *testing.T) {
	cfg := config.Config{Agents: map[string]config.AgentConfig{
		"pm": {Runner: "opencode", Model: "opencode-go/deepseek-v4.1-flash"},
	}}
	oc, ok := chatRunner(cfg).(runner.OpenCodeRunner)
	if !ok || oc.Model != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("expected opencode runner with PM model, got %#v", chatRunner(cfg))
	}
}

func TestChatRunner_AppliesPMEffort(t *testing.T) {
	cfg := config.Config{Agents: map[string]config.AgentConfig{
		"pm": {Runner: "opencode", Model: "m", Effort: "max"},
	}}
	if _, ok := chatRunner(cfg).(*runner.EffortRunner); !ok {
		t.Fatalf("expected effort-wrapped runner, got %T", chatRunner(cfg))
	}
}

func TestChatRunner_FallsBackOnUnknownRunner(t *testing.T) {
	cfg := config.Config{Agents: map[string]config.AgentConfig{"pm": {Runner: "bogus"}}}
	if _, ok := chatRunner(cfg).(runner.OpenCodeRunner); !ok {
		t.Fatal("expected opencode fallback")
	}
}
