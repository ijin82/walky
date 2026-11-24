# Walky — офлайн-«облако» на флешке

Двусторонняя синхронизация папки с USB-флешкой, которая работает как удалённый
сервер Dropbox/Яндекс.Диска: история версий, дедупликация, конфликты, восстановление
на новой машине. Интернет не нужен — транспорт физический.

Состояние: **M0 (каркас)**. Архитектура и план — [docs/DESIGN.md](docs/DESIGN.md).

## Структура

```
core/              Go-модуль "walky": демон, CLI, движок синхронизации
  cmd/walky/       точка входа и подкоманды
  internal/        config, version, api (D-Bus), далее store/scan/sync/device/index
ui/                Vala + GTK4/libadwaita: отдельный процесс, общается по D-Bus
  src/             Application.vala, Window.vala, main.vala
data/              systemd-юнит, udev, sysctl, пример конфига
docs/              DESIGN.md — архитектура, подводные камни, план
```

Соглашение: **весь код, идентификаторы и комментарии — на английском**; документация
и общение — на русском. Go и Vala не смешиваются в одном каталоге и собираются
независимо (`core/` — `go build`, `ui/` — `valac`).

## Сборка

```sh
make check          # gofmt + go vet + go test + сборка обоих бинарников
make build          # bin/walky, bin/walky-ui
make run-ui         # запустить окно
./bin/walky version
./bin/walky status --json
```

Нужны Go ≥ 1.24 (используются `log/slog`, далее `os.Root`) и Vala с vapi для
GTK4/libadwaita. Meson не требуется: UI собирается коротким `ui/Makefile`.

## Установка (после M3)

```sh
sudo make install install-data
systemctl --user daemon-reload
systemctl --user enable --now walky.service
```

`walky.service` — user-юнит с D-Bus-активацией; системный udev-режим — в
`data/99-walky-usb.rules` (закомментирован).

## Безопасность данных

Флешка — не бэкап. Пока не сделана вторая реплика, любая потеря носителя = потеря
данных. Целевая ФС — ext4 + LUKS2 (шифрование — стадия M8).
