import type { DownloadStatus, HealthStatus } from '@/shared/api/types';

export const ru = {
  title: 'Админка',
  brand: 'TapeNest',
  navOverview: 'Обзор',
  navUsers: 'Люди',
  navDownloads: 'Загрузки',
  navMusic: 'Музыка',
  refusedTitle: 'Нет доступа',
  refusedBody: 'Этот Telegram-аккаунт не в списке администраторов. Откройте плеер — админка для него закрыта.',
  openInTelegram: 'Откройте админку из Telegram.',
  loginFailed: 'Не удалось войти. Закройте мини-приложение и откройте снова.',
  loading: 'Загрузка…',
  retry: 'Повторить',
  empty: 'Пока пусто',
  error: 'Не удалось загрузить данные',
  healthTitle: 'Сервисы',
  usersTitle: 'Пользователи',
  likes: 'Лайки',
  recent: 'Слушали недавно',
  artists: 'Артисты',
  genres: 'Жанры',
  noPrefs: 'Предпочтений пока нет',
  downloadsTitle: 'Загрузки',
  all: 'Все',
  retryJob: 'Повторить',
  removeJob: 'Убрать из библиотеки',
  confirmRetryTitle: 'Повторить загрузку?',
  confirmRetryBody: 'Задание снова встанет в очередь.',
  confirmRemoveTitle: 'Убрать файл?',
  confirmRemoveBody: 'Готовый файл исчезнет из библиотеки этого пользователя. Общий файл других людей не трогаем.',
  cancel: 'Отмена',
  confirm: 'Подтвердить',
  catalogTitle: 'Каталог',
  searchPlaceholder: 'Поиск по каталогу',
  searchAction: 'Найти',
  searchHint: 'Тот же каталог, что в плеере. Новый источник не подключается.',
  back: 'Назад',
  themeLight: 'Светлая тема',
  themeDark: 'Тёмная тема',
  tracks: 'Треки',
  albums: 'Альбомы',
  artistsCount: 'Артисты',
  library: 'В библиотеке',
  youtube: 'YouTube Music',
  deleted: 'Скрытые',
  lastSync: 'Последняя синхронизация',
  never: 'ещё не было',
  hiddenNote: 'Показаны все задания, в том числе чужие — их владелец в своём списке не видит.',
} as const;

const SERVICE_NAMES: Record<string, string> = {
  gateway: 'Шлюз',
  bot: 'Бот',
  music: 'Музыка',
  downloads: 'Загрузки',
  recommendations: 'Рекомендации',
};

export function serviceName(id: string): string {
  return SERVICE_NAMES[id] ?? id;
}

export function healthLabel(status: HealthStatus): string {
  switch (status) {
    case 'ok':
      return 'работает';
    case 'down':
      return 'не отвечает';
    case 'off':
      return 'не подключён';
    default: {
      const unexpected: never = status;
      return unexpected;
    }
  }
}

export function downloadLabel(status: DownloadStatus): string {
  switch (status) {
    case 'queued':
      return 'В очереди';
    case 'running':
      return 'Идёт';
    case 'done':
      return 'Готово';
    case 'failed':
      return 'Ошибка';
    default: {
      const unexpected: never = status;
      return unexpected;
    }
  }
}

export function displayName(user: { firstName: string; lastName: string | null; username: string | null }): string {
  const name = [user.firstName, user.lastName].filter(Boolean).join(' ').trim();
  if (name) return name;
  if (user.username) return `@${user.username}`;
  return 'Без имени';
}

export function roleLabel(role: string): string {
  switch (role) {
    case 'admin':
      return 'админ';
    case 'user':
      return 'слушатель';
    default:
      return role;
  }
}
