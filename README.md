# Walky — офлайн-«облако» на флешке

Двусторонняя синхронизация папки с USB-флешкой, которая работает как удалённый
сервер Dropbox/Яндекс.Диска: история версий, дедупликация, конфликты, восстановление
на новой машине. Интернет не нужен — транспорт физический.

Состояние: **M0 (каркас)**. Архитектура и план — [docs/DESIGN.md](docs/DESIGN.md).

## Сборка

```sh
make build          # bin/walky
make test vet
./bin/walky version
./bin/walky status --json
```

Нужен Go ≥ 1.24 (используется `log/slog`, `os.Root` — со стадии M1).

## Установка (после M3)

```sh
sudo make install install-data
systemctl --user daemon-reload
systemctl --user enable --now walky.service
```

`walky.service` — user-юнит с D-Bus-активацией; системный udev-режим — в
`data/99-walky-usb.rules` (закомментирован).

## Раскладка

```
cmd/walky/         CLI и демон
internal/config/   конфиг (TOML) + валидация
internal/version/  версия сборки
internal/api/      D-Bus-интерфейс (XML)
data/              systemd, udev, sysctl
docs/DESIGN.md     архитектура, подводные камни, план
```

## Безопасность данных

Флешка — не бэкап. Пока не сделана вторая реплика, любая потеря носителя = потеря
данных. Шифрование (LUKS2) планируется в M8; целевая ФС — ext4.
