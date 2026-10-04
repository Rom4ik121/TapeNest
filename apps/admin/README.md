# Админка TapeNest

Отдельное Telegram mini app (`/admin/`, порт 5175). Это не экран плеера и не MediaHub.

Кнопка и команда `/admin` появляются у бота только когда задан `MINIAPP_ADMIN_URL`. Кнопка меню остаётся плеером.

Доступ есть только у Telegram user id из `ADMIN_TELEGRAM_IDS` (корневой `.env.example`). Пустой список никого не пускает. Настоящие id и токены сюда не кладём.

```bash
npm ci
npm test
npm run dev    # http://localhost:5175/admin/
```
