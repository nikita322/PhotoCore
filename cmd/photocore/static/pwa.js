// PhotoCore PWA Module
// Управление Service Worker, установкой, оффлайн режимом и кэшем

(function() {
  'use strict';

  let swRegistration = null;
  let deferredPrompt = null;

  // === Service Worker Registration ===
  if ('serviceWorker' in navigator) {
    window.addEventListener('load', async () => {
      try {
        swRegistration = await navigator.serviceWorker.register('/static/sw.js', {
          scope: '/'
        });
        console.log('[PWA] Service Worker registered with scope:', swRegistration.scope);

        // Проверка обновлений каждые 24 часа
        setInterval(() => {
          swRegistration.update();
        }, 24 * 60 * 60 * 1000);

        // Слушаем обновления SW
        swRegistration.addEventListener('updatefound', () => {
          const newWorker = swRegistration.installing;

          newWorker.addEventListener('statechange', () => {
            if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
              // Новая версия готова
              console.log('[PWA] New version available');

              if (window.showToast) {
                showToast('Доступно обновление. Перезагрузите страницу.', 'info');
              }

              // Автоматически активируем новую версию
              newWorker.postMessage({ type: 'SKIP_WAITING' });
            }
          });
        });

        // Перезагружаем страницу при активации нового SW
        let refreshing = false;
        navigator.serviceWorker.addEventListener('controllerchange', () => {
          if (!refreshing) {
            refreshing = true;
            window.location.reload();
          }
        });

      } catch (error) {
        console.error('[PWA] Service Worker registration failed:', error);
      }
    });
  }

  // === Install Prompt (Add to Home Screen) ===
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault();
    deferredPrompt = e;

    console.log('[PWA] Install prompt ready');

    // Показываем кнопку установки
    const installBtn = document.getElementById('install-btn');
    if (installBtn) {
      installBtn.style.display = 'block';
    }
  });

  // Функция установки PWA
  async function installPWA() {
    if (!deferredPrompt) {
      console.log('[PWA] Install prompt not available');
      return;
    }

    deferredPrompt.prompt();
    const { outcome } = await deferredPrompt.userChoice;

    console.log('[PWA] Install outcome:', outcome);

    if (outcome === 'accepted') {
      if (window.showToast) {
        showToast('Приложение устанавливается...', 'success');
      }
    }

    deferredPrompt = null;

    // Скрываем кнопку установки
    const installBtn = document.getElementById('install-btn');
    if (installBtn) {
      installBtn.style.display = 'none';
    }
  }

  // Событие успешной установки
  window.addEventListener('appinstalled', () => {
    console.log('[PWA] App installed');

    if (window.showToast) {
      showToast('Приложение успешно установлено!', 'success');
    }

    // Запрашиваем persistent storage
    requestPersistentStorage();
  });

  // === Offline Toast Management ===
  let offlineToast = null;
  let toastText = null;
  let toastReloadBtn = null;
  let toastIcon = null;
  let checkInterval = null;
  let isOffline = false;
  let lastOnlineState = true; // По умолчанию считаем что онлайн

  function initOfflineToast() {
    offlineToast = document.getElementById('offline-toast');
    toastText = document.getElementById('toast-text');
    toastReloadBtn = document.getElementById('toast-reload-btn');
    toastIcon = document.getElementById('toast-icon');
  }

  function updateToastIcon(online) {
    if (toastIcon) {
      if (online) {
        toastIcon.innerHTML = '<path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z"/>';
      } else {
        toastIcon.innerHTML = '<path d="M23.64 7c-.45-.34-4.93-4-11.64-4-1.5 0-2.89.19-4.15.48L18.18 13.8 23.64 7zM17.04 15.22L3.27 1.44 2 2.72l2.05 2.06C1.91 5.76.59 6.82.36 7l11.63 14.49.01.01.01-.01 3.9-4.86 3.32 3.32 1.27-1.27-3.46-3.46z"/>';
      }
    }
  }

  function showOfflineToast() {
    if (!offlineToast) return;

    isOffline = true;
    offlineToast.classList.add('visible');
    offlineToast.classList.remove('online');
    updateToastIcon(false);
    if (toastText) toastText.textContent = 'Нет соединения - работаем оффлайн';
    if (toastReloadBtn) toastReloadBtn.style.display = 'none';

    // Запускаем проверку каждые 10 секунд
    if (!checkInterval) {
      checkInterval = setInterval(checkConnection, 10000);
    }
  }

  function showOnlineToast() {
    if (!offlineToast) return;

    isOffline = false;
    offlineToast.classList.add('visible');
    offlineToast.classList.add('online');
    updateToastIcon(true);
    if (toastText) toastText.textContent = 'Соединение восстановлено';
    if (toastReloadBtn) toastReloadBtn.style.display = 'inline-block';

    // Останавливаем проверку
    if (checkInterval) {
      clearInterval(checkInterval);
      checkInterval = null;
    }
  }

  function hideToast() {
    if (offlineToast) {
      offlineToast.classList.remove('visible');
    }
  }

  // Реальная проверка подключения к интернету
  async function checkRealConnection() {
    try {
      // Используем GET запрос к статическому файлу с уникальным параметром
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 3000);

      const response = await fetch(`/static/manifest.json?_t=${Date.now()}`, {
        method: 'GET',
        cache: 'no-store',
        signal: controller.signal
      });

      clearTimeout(timeoutId);
      return response.ok;
    } catch (error) {
      return false;
    }
  }

  async function checkConnection() {
    const isOnline = await checkRealConnection();

    if (isOnline && isOffline) {
      // Соединение восстановилось
      showOnlineToast();
      lastOnlineState = true;
    }
  }

  async function updateOfflineStatus() {
    const isOnline = await checkRealConnection();

    // Обновляем статус только если он изменился
    if (!isOnline && !isOffline) {
      // Переход в оффлайн
      showOfflineToast();
      lastOnlineState = false;
    } else if (isOnline && isOffline) {
      // Переход в онлайн
      showOnlineToast();
      lastOnlineState = true;
    } else if (isOnline) {
      // Онлайн и был онлайн - ничего не делаем
      hideToast();
      lastOnlineState = true;
    }
  }

  // === Offline Detection ===
  window.addEventListener('online', () => {
    console.log('[PWA] Online');

    // Обновляем offline toast
    updateOfflineStatus();

    // Синхронизируем очередь загрузки
    if (swRegistration && 'sync' in swRegistration) {
      swRegistration.sync.register('upload-queue').catch((error) => {
        console.warn('[PWA] Background sync not available:', error);
      });
    }
  });

  window.addEventListener('offline', () => {
    console.log('[PWA] Offline');

    // Обновляем offline toast
    updateOfflineStatus();
  });

  // === Cache Management ===

  // Кэширование избранного для оффлайн просмотра
  async function cacheFavorites(limit = 50) {
    console.log('[PWA] cacheFavorites called with limit:', limit);

    if (window.showToast) {
      showToast('Кэширование избранного...', 'info');
    }

    try {
      // Используем глобальную переменную swRegistration или ждём готовности
      console.log('[PWA] Checking service worker...');
      let registration = swRegistration;

      if (!registration) {
        console.log('[PWA] No swRegistration, waiting for ready...');
        registration = await navigator.serviceWorker.ready;
      }

      // Проверяем что есть активный SW
      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) {
        throw new Error('Service Worker не найден');
      }

      console.log('[PWA] Service worker state:', activeWorker.state);

      // Получаем список избранного
      console.log('[PWA] Fetching favorites...');
      const response = await fetch(`/api/favorites?limit=${limit || 0}`);
      if (!response.ok) {
        throw new Error('Не удалось загрузить избранное');
      }

      const favorites = await response.json();
      console.log('[PWA] Favorites received:', favorites.length);
      const mediaIds = favorites.map(m => m.id);
      console.log('[PWA] Media IDs:', mediaIds);

      if (mediaIds.length === 0) {
        if (window.showToast) {
          showToast('Нет избранных фото для кэширования', 'warning');
        }
        return 0;
      }

      // Отправляем в Service Worker
      console.log('[PWA] Sending message to SW...');
      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();

        messageChannel.port1.onmessage = (event) => {
          console.log('[PWA] Received response from SW:', event.data);
          if (event.data.success) {
            if (window.showToast) {
              showToast(`Закэшировано ${event.data.count} фото`, 'success');
            }
            resolve(event.data.count);
          } else {
            reject(new Error(event.data.error || 'Ошибка кэширования'));
          }
        };

        const message = {
          type: 'CACHE_FAVORITES',
          data: { mediaIds }
        };
        console.log('[PWA] Message to send:', message);

        activeWorker.postMessage(message, [messageChannel.port2]);
        console.log('[PWA] Message sent to SW');
      });
    } catch (error) {
      console.error('[PWA] Cache favorites error:', error);

      if (window.showToast) {
        showToast('Ошибка кэширования: ' + error.message, 'error');
      }

      throw error;
    }
  }

  // Очистка оффлайн кэша
  async function clearOfflineCache() {
    try {
      let registration = swRegistration;
      if (!registration) {
        registration = await navigator.serviceWorker.ready;
      }

      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) {
        throw new Error('Service Worker не найден');
      }

      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();

        messageChannel.port1.onmessage = (event) => {
          if (event.data.success) {
            if (window.showToast) {
              showToast('Кэш очищен', 'success');
            }
            resolve();
          } else {
            reject(new Error(event.data.error || 'Ошибка очистки'));
          }
        };

        activeWorker.postMessage({
          type: 'CLEAR_CACHE'
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Clear cache error:', error);
      throw new Error('Service Worker не активен');
    }
  }

  // Получение размера кэша
  async function getCacheSize() {
    try {
      let registration = swRegistration;
      if (!registration) {
        registration = await navigator.serviceWorker.ready;
      }

      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) {
        return 0;
      }

      return new Promise((resolve) => {
        const messageChannel = new MessageChannel();

        messageChannel.port1.onmessage = (event) => {
          resolve(event.data.size || 0);
        };

        activeWorker.postMessage({
          type: 'GET_CACHE_SIZE'
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Get cache size error:', error);
      return 0;
    }
  }

  // === Storage Persistence ===
  async function requestPersistentStorage() {
    if (!navigator.storage || !navigator.storage.persist) {
      console.log('[PWA] Storage API not available');
      return false;
    }

    const isPersisted = await navigator.storage.persisted();

    if (isPersisted) {
      console.log('[PWA] Storage already persistent');
      return true;
    }

    const result = await navigator.storage.persist();

    if (result) {
      console.log('[PWA] Storage is now persistent');

      if (window.showToast) {
        showToast('Хранилище защищено от автоочистки', 'success');
      }
    } else {
      console.log('[PWA] Storage persistence denied');
    }

    return result;
  }

  // === Upload Queue Management ===

  // Добавить файл в очередь загрузки (для оффлайн режима)
  async function addToUploadQueue(file, metadata = {}) {
    try {
      let registration = swRegistration;
      if (!registration) registration = await navigator.serviceWorker.ready;
      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) throw new Error('Service Worker не найден');

      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();
        messageChannel.port1.onmessage = (event) => {
          if (event.data.success) {
            console.log('[PWA] Added to upload queue:', file.name, 'ID:', event.data.id);
            resolve(event.data.id);
          } else {
            reject(new Error(event.data.error || 'Ошибка добавления в очередь'));
          }
        };
        activeWorker.postMessage({
          type: 'ADD_TO_UPLOAD_QUEUE',
          data: { file, metadata }
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Add to upload queue error:', error);
      throw new Error('Service Worker не активен');
    }
  }

  // Получить очередь загрузки
  async function getUploadQueue() {
    try {
      let registration = swRegistration;
      if (!registration) registration = await navigator.serviceWorker.ready;
      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) return [];

      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();
        messageChannel.port1.onmessage = (event) => {
          if (event.data.success) {
            resolve(event.data.queue || []);
          } else {
            reject(new Error(event.data.error || 'Ошибка получения очереди'));
          }
        };
        activeWorker.postMessage({
          type: 'GET_UPLOAD_QUEUE'
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Get upload queue error:', error);
      return [];
    }
  }

  // Очистить очередь загрузки
  async function clearUploadQueue() {
    try {
      let registration = swRegistration;
      if (!registration) registration = await navigator.serviceWorker.ready;
      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) throw new Error('Service Worker не найден');

      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();
        messageChannel.port1.onmessage = (event) => {
          if (event.data.success) {
            console.log('[PWA] Upload queue cleared');
            resolve();
          } else {
            reject(new Error(event.data.error || 'Ошибка очистки очереди'));
          }
        };
        activeWorker.postMessage({
          type: 'CLEAR_UPLOAD_QUEUE'
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Clear upload queue error:', error);
      throw new Error('Service Worker не активен');
    }
  }

  // Обработать очередь загрузки (fallback для iOS)
  async function processUploadQueue() {
    try {
      let registration = swRegistration;
      if (!registration) registration = await navigator.serviceWorker.ready;
      const activeWorker = registration.active || registration.waiting || registration.installing;
      if (!activeWorker) throw new Error('Service Worker не найден');

      return new Promise((resolve, reject) => {
        const messageChannel = new MessageChannel();
        messageChannel.port1.onmessage = (event) => {
          if (event.data.success) {
            console.log('[PWA] Upload queue processed');
            resolve();
          } else {
            reject(new Error(event.data.error || 'Ошибка обработки очереди'));
          }
        };
        activeWorker.postMessage({
          type: 'PROCESS_UPLOAD_QUEUE'
        }, [messageChannel.port2]);
      });
    } catch (error) {
      console.error('[PWA] Process upload queue error:', error);
      throw new Error('Service Worker не активен');
    }
  }

  // Проверка поддержки Background Sync
  async function supportsBackgroundSync() {
    if (!('serviceWorker' in navigator)) {
      return false;
    }
    try {
      let registration = swRegistration;
      if (!registration) registration = await navigator.serviceWorker.ready;
      return 'sync' in registration;
    } catch (error) {
      return false;
    }
  }

  // === Share API ===
  async function sharePhoto(mediaId, filename) {
    if (!navigator.share) {
      console.warn('[PWA] Share API not supported');

      if (window.showToast) {
        showToast('Share API не поддерживается', 'warning');
      }

      return false;
    }

    try {
      const response = await fetch(`/media/${mediaId}`);
      const blob = await response.blob();
      const file = new File([blob], filename, { type: blob.type });

      await navigator.share({
        files: [file],
        title: filename,
        text: 'Фото из PhotoCore'
      });

      return true;
    } catch (error) {
      if (error.name !== 'AbortError') {
        console.error('[PWA] Share failed:', error);
      }
      return false;
    }
  }

  // === Export API ===
  window.PWA = {
    installPWA,
    cacheFavorites,
    clearOfflineCache,
    getCacheSize,
    requestPersistentStorage,
    sharePhoto,
    addToUploadQueue,
    getUploadQueue,
    clearUploadQueue,
    processUploadQueue,
    supportsBackgroundSync,
    isOnline: () => navigator.onLine,
    isInstalled: () => window.matchMedia('(display-mode: standalone)').matches ||
                       window.navigator.standalone === true
  };

  // Инициализация при загрузке
  console.log('[PWA] Module loaded');

  // Проверяем состояние online/offline
  if (!navigator.onLine) {
    console.log('[PWA] Currently offline');
  }

  // Проверяем, запущено ли как PWA
  if (window.PWA.isInstalled()) {
    console.log('[PWA] Running as installed app');
    document.body.classList.add('pwa-installed');
  }

  // === Инициализация Offline Toast ===
  // Инициализируем сразу при готовности DOM
  console.log('[PWA] Initializing offline toast, document.readyState:', document.readyState);
  if (document.readyState === 'loading') {
    console.log('[PWA] Waiting for DOMContentLoaded');
    document.addEventListener('DOMContentLoaded', () => {
      console.log('[PWA] DOMContentLoaded fired');
      initOfflineToast();
      updateOfflineStatus();
    });
  } else {
    // DOM уже готов
    console.log('[PWA] DOM already ready, initializing now');
    initOfflineToast();
    updateOfflineStatus();
  }

})();
