package logger

import (
	"go.uber.org/zap"
)

// New создаёт логер с нужным уровнем логирования. Глобального логера нет:
// логер создаётся в main и передаётся в компоненты явно, а каждый компонент
// получает дочерний логер через With (например, с полем component).
func New(level string) (*zap.Logger, error) {
	// преобразуем текстовый уровень логирования в zap.AtomicLevel
	lvl, err := zap.ParseAtomicLevel(level)
	if err != nil {
		return nil, err
	}
	// создаём новую конфигурацию логера
	cfg := zap.NewProductionConfig()
	// устанавливаем уровень
	cfg.Level = lvl
	// отключаем стектрейс: он одинаков для однотипных ошибок (например, сетевых)
	// и не несёт диагностической пользы сверх поля caller
	cfg.DisableStacktrace = true
	// создаём логер на основе конфигурации
	return cfg.Build()
}
