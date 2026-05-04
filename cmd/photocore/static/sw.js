// PhotoCore Service Worker
// Version: 1.6.0

const CACHE_VERSION = 'v7';
const CACHE_STATIC = `photocore-static-${CACHE_VERSION}`;
const CACHE_MEDIA = `photocore-media-${CACHE_VERSION}`;
const CACHE_FAVORITES = `photocore-favorites-${CACHE_VERSION}`;
const CACHE_RUNTIME = `photocore-runtime-${CACHE_VERSION}`;

// Статические ресурсы для кэширования при установке
const STATIC_ASSETS = [
  '/static/manifest.json',
  '/static/images/logo.svg',
  '/static/pwa.js',
  '/static/offline.html',
  '/static/js/htmx.min.js'
];

// === Install Event ===
self.addEventListener('install', (event) => {
  console.log('[SW] Install event');

  event.waitUntil(
    caches.open(CACHE_STATIC)
      .then(async (cache) => {
        console.log('[SW] Caching static assets');

        // Кешируем ресурсы по одному, игнорируя ошибки
        const promises = STATIC_ASSETS.map(async (url) => {
          try {
            const response = await fetch(url);
            if (response.ok) {
              await cache.put(url, response);
              console.log(`[SW] Cached: ${url}`);
            } else {
              console.warn(`[SW] Failed to cache ${url}: ${response.status}`);
            }
          } catch (error) {
            console.warn(`[SW] Error caching ${url}:`, error);
          }
        });

        await Promise.all(promises);
        console.log('[SW] Static assets cached');
      })
      .then(() => {
        console.log('[SW] Skip waiting');
        return self.skipWaiting();
      })
  );
});

// === Activate Event ===
self.addEventListener('activate', (event) => {
  console.log('[SW] Activate event');

  event.waitUntil(
    caches.keys()
      .then((cacheNames) => {
        return Promise.all(
          cacheNames.map((cacheName) => {
            // Удаляем старые версии кэша
            if (!cacheName.includes(CACHE_VERSION)) {
              console.log('[SW] Deleting old cache:', cacheName);
              return caches.delete(cacheName);
            }
          })
        );
      })
      .then(() => {
        console.log('[SW] Claim clients');
        return self.clients.claim();
      })
  );
});

// === Fetch Event (Routing Logic) ===
self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);

  console.log('[SW] Fetch:', request.method, url.pathname, 'mode:', request.mode);

  // Игнорируем не-GET запросы
  if (request.method !== 'GET') {
    console.log('[SW] Ignoring non-GET request');
    return;
  }

  // Игнорируем chrome-extension и другие протоколы
  if (!url.protocol.startsWith('http')) {
    console.log('[SW] Ignoring non-http protocol');
    return;
  }

  // API requests - Network First
  if (url.pathname.startsWith('/api/')) {
    event.respondWith(networkFirst(request, CACHE_RUNTIME));
    return;
  }

  // Media files (превью и оригиналы) - Cache First
  if (url.pathname.startsWith('/media/')) {
    event.respondWith(cacheFirst(request, CACHE_MEDIA));
    return;
  }

  // Static files - Stale While Revalidate
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(staleWhileRevalidate(request, CACHE_STATIC));
    return;
  }

  // HTML pages - Network First с offline fallback
  if (request.mode === 'navigate' ||
      request.headers.get('accept')?.includes('text/html')) {
    console.log('[SW] HTML page detected, using networkFirstWithOffline');
    event.respondWith(networkFirstWithOffline(request));
    return;
  }

  // Default - Network First
  event.respondWith(networkFirst(request, CACHE_RUNTIME));
});

// === Caching Strategies ===

// Cache First - сначала кэш, потом сеть (для медиа)
async function cacheFirst(request, cacheName) {
  try {
    const cache = await caches.open(cacheName);
    const cached = await cache.match(request);

    if (cached) {
      console.log('[SW] Cache hit:', request.url);
      return cached;
    }

    console.log('[SW] Cache miss, fetching:', request.url);
    const response = await fetch(request);

    // Кэшируем только успешные ответы
    // НЕ кэшируем если сервер сказал no-store (placeholder'ы)
    const cacheControl = response.headers.get('Cache-Control');
    if (response.ok && response.status === 200 &&
        (!cacheControl || !cacheControl.includes('no-store'))) {
      cache.put(request, response.clone());
      console.log('[SW] Cached response:', request.url);
    } else if (cacheControl && cacheControl.includes('no-store')) {
      console.log('[SW] NOT caching (no-store):', request.url);
    }

    return response;
  } catch (error) {
    console.error('[SW] Cache first failed:', error);

    // Пытаемся вернуть из кэша даже если запрос упал
    const cache = await caches.open(cacheName);
    const cached = await cache.match(request);

    if (cached) {
      return cached;
    }

    // Возвращаем offline placeholder для изображений
    if (request.destination === 'image') {
      return new Response(
        '<svg xmlns="http://www.w3.org/2000/svg" width="300" height="300" viewBox="0 0 300 300"><rect fill="#1c1c1c" width="300" height="300"/><text x="150" y="150" font-family="Arial" font-size="14" fill="#938f99" text-anchor="middle" dominant-baseline="middle">Offline</text></svg>',
        { headers: { 'Content-Type': 'image/svg+xml' } }
      );
    }

    throw error;
  }
}

// Network First - сначала сеть, потом кэш (для API и динамического контента)
async function networkFirst(request, cacheName) {
  try {
    const response = await fetch(request);

    // Кэшируем успешные ответы
    if (response.ok && response.status === 200) {
      const cache = await caches.open(cacheName);
      cache.put(request, response.clone());
    }

    return response;
  } catch (error) {
    console.warn('[SW] Network failed, trying cache:', request.url);

    const cache = await caches.open(cacheName);
    const cached = await cache.match(request);

    if (cached) {
      console.log('[SW] Serving from cache:', request.url);
      return cached;
    }

    throw error;
  }
}

// Network First с offline fallback (для HTML страниц)
async function networkFirstWithOffline(request) {
  const url = new URL(request.url);
  console.log('[SW] Navigation request:', url.pathname);

  try {
    console.log('[SW] Trying network...');
    const response = await fetch(request);

    console.log('[SW] Network response:', response.status);

    // Кэшируем успешные ответы
    if (response.ok && response.status === 200) {
      const cache = await caches.open(CACHE_RUNTIME);
      cache.put(request, response.clone());
      console.log('[SW] Cached page:', url.pathname);
    }

    return response;
  } catch (error) {
    console.warn('[SW] Network failed:', error.message);
    console.log('[SW] Trying cache...');

    // Пытаемся вернуть из кэша runtime
    const runtimeCache = await caches.open(CACHE_RUNTIME);
    const cached = await runtimeCache.match(request);

    if (cached) {
      console.log('[SW] Serving from runtime cache:', url.pathname);
      return cached;
    }

    // Пытаемся найти offline.html в любом кеше
    console.log('[SW] No cache found, trying offline.html...');
    const cacheNames = await caches.keys();

    for (const cacheName of cacheNames) {
      const cache = await caches.open(cacheName);
      const offlinePage = await cache.match('/static/offline.html');

      if (offlinePage) {
        console.log('[SW] Serving offline.html from:', cacheName);
        return offlinePage;
      }
    }

    // Последний резерв - создаем простую offline страницу
    console.error('[SW] No offline page in cache, creating fallback');
    return new Response(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <title>Оффлайн - PhotoCore</title>
        <style>
          body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #121212;
            color: #e6e1e5;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            padding: 2rem;
            text-align: center;
          }
          h1 { margin-bottom: 1rem; }
          button {
            padding: 0.875rem 2rem;
            background: #8ab4f8;
            color: #003258;
            border: none;
            border-radius: 24px;
            font-weight: 600;
            cursor: pointer;
            margin-top: 1rem;
          }
        </style>
      </head>
      <body>
        <div>
          <h1>Нет подключения</h1>
          <p>Вы находитесь оффлайн. Проверьте подключение к интернету.</p>
          <button onclick="location.reload()">Повторить</button>
        </div>
      </body>
      </html>
    `, {
      status: 503,
      statusText: 'Service Unavailable',
      headers: { 'Content-Type': 'text/html; charset=utf-8' }
    });
  }
}

// Stale While Revalidate - возвращаем кэш сразу, обновляем в фоне
async function staleWhileRevalidate(request, cacheName) {
  const cache = await caches.open(cacheName);
  const cached = await cache.match(request);

  // Запускаем fetch в фоне для обновления
  const fetchPromise = fetch(request)
    .then((response) => {
      if (response.ok && response.status === 200) {
        cache.put(request, response.clone());
      }
      return response;
    })
    .catch((error) => {
      console.warn('[SW] Background fetch failed:', error);
    });

  // Возвращаем кэш сразу, если есть
  if (cached) {
    return cached;
  }

  // Если кэша нет, ждём сетевой запрос
  return fetchPromise;
}

// === Message Event (управление кэшем и очередью из UI) ===
self.addEventListener('message', async (event) => {
  const { type, data } = event.data;

  console.log('[SW] Message received:', type);

  try {
    switch (type) {
      case 'CACHE_FAVORITES':
        console.log('[SW] CACHE_FAVORITES received, data:', data);
        console.log('[SW] mediaIds:', data.mediaIds);
        console.log('[SW] event.ports:', event.ports);
        await cacheFavorites(data.mediaIds, data.size || 'small');
        console.log('[SW] cacheFavorites completed, sending response');
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true, count: data.mediaIds.length });
          console.log('[SW] Response sent');
        } else {
          console.error('[SW] No ports available to send response!');
        }
        break;

      case 'CLEAR_CACHE':
        await clearCache(data.cacheName || CACHE_FAVORITES);
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true });
        }
        break;

      case 'GET_CACHE_SIZE':
        const size = await getCacheSize();
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ size });
        }
        break;

      case 'SKIP_WAITING':
        self.skipWaiting();
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true });
        }
        break;

      case 'ADD_TO_UPLOAD_QUEUE':
        const id = await addToUploadQueue(data.file, data.metadata);
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true, id });
        }
        break;

      case 'GET_UPLOAD_QUEUE':
        const queue = await getUploadQueue();
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true, queue });
        }
        break;

      case 'CLEAR_UPLOAD_QUEUE':
        const db = await openDatabase();
        const transaction = db.transaction([STORE_UPLOAD_QUEUE], 'readwrite');
        const store = transaction.objectStore(STORE_UPLOAD_QUEUE);
        await store.clear();
        db.close();
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true });
        }
        break;

      case 'PROCESS_UPLOAD_QUEUE':
        await processUploadQueue();
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ success: true });
        }
        break;

      default:
        console.warn('[SW] Unknown message type:', type);
        if (event.ports && event.ports[0]) {
          event.ports[0].postMessage({ error: 'Unknown message type' });
        }
    }
  } catch (error) {
    console.error('[SW] Message handler error:', error);
    if (event.ports && event.ports[0]) {
      event.ports[0].postMessage({ error: error.message });
    }
  }
});

// === Helper Functions ===

// Кэширование избранного для оффлайн просмотра
async function cacheFavorites(mediaIds, size = 'small') {
  console.log(`[SW] cacheFavorites started, mediaIds count: ${mediaIds ? mediaIds.length : 0}`);

  if (!mediaIds || mediaIds.length === 0) {
    console.warn('[SW] No mediaIds provided');
    return;
  }

  const cache = await caches.open(CACHE_FAVORITES);
  console.log('[SW] Cache opened:', CACHE_FAVORITES);

  const promises = [];

  for (const id of mediaIds) {
    const thumbUrl = `/media/${id}/thumb/${size}`;

    const promise = fetch(thumbUrl)
      .then((response) => {
        if (response.ok) {
          console.log(`[SW] Caching ${thumbUrl}`);
          return cache.put(thumbUrl, response);
        } else {
          console.warn(`[SW] Failed to fetch ${thumbUrl}: ${response.status}`);
        }
      })
      .catch((error) => {
        console.warn(`[SW] Failed to cache ${id}:`, error);
      });

    promises.push(promise);
  }

  await Promise.all(promises);
  console.log(`[SW] Cached ${mediaIds.length} favorites`);
}

// Очистка кэша
async function clearCache(cacheName) {
  const deleted = await caches.delete(cacheName);
  console.log(`[SW] Cache ${cacheName} cleared:`, deleted);
  return deleted;
}

// Получение размера кэша
async function getCacheSize() {
  if ('storage' in navigator && 'estimate' in navigator.storage) {
    const estimate = await navigator.storage.estimate();
    return estimate.usage || 0;
  }
  return 0;
}

// === IndexedDB Helper Functions ===

const DB_NAME = 'photocore-db';
const DB_VERSION = 1;
const STORE_UPLOAD_QUEUE = 'uploadQueue';
const STORE_SETTINGS = 'settings';

// Открытие/создание базы данных
function openDatabase() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);

    request.onerror = () => {
      console.error('[SW] IndexedDB error:', request.error);
      reject(request.error);
    };

    request.onsuccess = () => {
      console.log('[SW] IndexedDB opened');
      resolve(request.result);
    };

    request.onupgradeneeded = (event) => {
      console.log('[SW] IndexedDB upgrade needed');
      const db = event.target.result;

      // Создаем object store для очереди загрузки
      if (!db.objectStoreNames.contains(STORE_UPLOAD_QUEUE)) {
        const uploadStore = db.createObjectStore(STORE_UPLOAD_QUEUE, {
          keyPath: 'id',
          autoIncrement: true
        });
        uploadStore.createIndex('timestamp', 'timestamp', { unique: false });
        uploadStore.createIndex('status', 'status', { unique: false });
        console.log('[SW] Created uploadQueue store');
      }

      // Создаем object store для настроек
      if (!db.objectStoreNames.contains(STORE_SETTINGS)) {
        db.createObjectStore(STORE_SETTINGS, { keyPath: 'key' });
        console.log('[SW] Created settings store');
      }
    };
  });
}

// Добавить файл в очередь загрузки
async function addToUploadQueue(file, metadata = {}) {
  const db = await openDatabase();

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([STORE_UPLOAD_QUEUE], 'readwrite');
    const store = transaction.objectStore(STORE_UPLOAD_QUEUE);

    const item = {
      file: file,
      fileName: file.name,
      fileSize: file.size,
      fileType: file.type,
      timestamp: Date.now(),
      status: 'pending',
      metadata: metadata,
      retries: 0
    };

    const request = store.add(item);

    request.onsuccess = () => {
      console.log('[SW] Added to upload queue:', file.name);
      resolve(request.result);
    };

    request.onerror = () => {
      console.error('[SW] Failed to add to queue:', request.error);
      reject(request.error);
    };

    transaction.oncomplete = () => {
      db.close();
    };
  });
}

// Получить все файлы из очереди
async function getUploadQueue() {
  const db = await openDatabase();

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([STORE_UPLOAD_QUEUE], 'readonly');
    const store = transaction.objectStore(STORE_UPLOAD_QUEUE);
    const request = store.getAll();

    request.onsuccess = () => {
      resolve(request.result);
    };

    request.onerror = () => {
      reject(request.error);
    };

    transaction.oncomplete = () => {
      db.close();
    };
  });
}

// Удалить файл из очереди
async function removeFromUploadQueue(id) {
  const db = await openDatabase();

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([STORE_UPLOAD_QUEUE], 'readwrite');
    const store = transaction.objectStore(STORE_UPLOAD_QUEUE);
    const request = store.delete(id);

    request.onsuccess = () => {
      console.log('[SW] Removed from queue:', id);
      resolve();
    };

    request.onerror = () => {
      reject(request.error);
    };

    transaction.oncomplete = () => {
      db.close();
    };
  });
}

// Обновить статус файла в очереди
async function updateQueueItemStatus(id, status, error = null) {
  const db = await openDatabase();

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([STORE_UPLOAD_QUEUE], 'readwrite');
    const store = transaction.objectStore(STORE_UPLOAD_QUEUE);
    const getRequest = store.get(id);

    getRequest.onsuccess = () => {
      const item = getRequest.result;
      if (!item) {
        reject(new Error('Item not found'));
        return;
      }

      item.status = status;
      if (error) {
        item.error = error;
      }
      if (status === 'uploading') {
        item.uploadStarted = Date.now();
      }

      const updateRequest = store.put(item);

      updateRequest.onsuccess = () => {
        resolve();
      };

      updateRequest.onerror = () => {
        reject(updateRequest.error);
      };
    };

    getRequest.onerror = () => {
      reject(getRequest.error);
    };

    transaction.oncomplete = () => {
      db.close();
    };
  });
}

// === Background Sync Event ===

self.addEventListener('sync', (event) => {
  console.log('[SW] Sync event:', event.tag);

  if (event.tag === 'upload-queue') {
    event.waitUntil(processUploadQueue());
  }
});

// Обработка очереди загрузки
async function processUploadQueue() {
  console.log('[SW] Processing upload queue...');

  try {
    const queue = await getUploadQueue();
    const pendingItems = queue.filter(item => item.status === 'pending');

    if (pendingItems.length === 0) {
      console.log('[SW] Upload queue is empty');
      return;
    }

    console.log(`[SW] Found ${pendingItems.length} files to upload`);

    let successCount = 0;
    let errorCount = 0;

    for (const item of pendingItems) {
      try {
        await updateQueueItemStatus(item.id, 'uploading');

        // Создаем FormData для загрузки
        const formData = new FormData();
        formData.append('files', item.file);

        // Выполняем загрузку
        const response = await fetch('/api/upload', {
          method: 'POST',
          body: formData,
          // Добавляем credentials для передачи cookies/tokens
          credentials: 'same-origin'
        });

        if (!response.ok) {
          throw new Error(`Upload failed: ${response.status} ${response.statusText}`);
        }

        const result = await response.json();
        console.log('[SW] Upload successful:', item.fileName, result);

        // Удаляем из очереди после успешной загрузки
        await removeFromUploadQueue(item.id);
        successCount++;

        // Показываем уведомление об успехе
        if (Notification.permission === 'granted') {
          self.registration.showNotification('Загрузка завершена', {
            body: `${item.fileName} успешно загружено`,
            icon: '/static/images/icons/icon-192.png',
            badge: '/static/images/icons/icon-72.png',
            tag: 'upload-success'
          });
        }
      } catch (error) {
        console.error('[SW] Upload failed:', item.fileName, error);

        // Увеличиваем счетчик попыток
        item.retries = (item.retries || 0) + 1;

        // Если превысили лимит попыток (3), помечаем как failed
        if (item.retries >= 3) {
          await updateQueueItemStatus(item.id, 'failed', error.message);

          // Показываем уведомление об ошибке
          if (Notification.permission === 'granted') {
            self.registration.showNotification('Ошибка загрузки', {
              body: `${item.fileName}: ${error.message}`,
              icon: '/static/images/icons/icon-192.png',
              badge: '/static/images/icons/icon-72.png',
              tag: 'upload-error'
            });
          }
        } else {
          // Возвращаем в статус pending для повторной попытки
          await updateQueueItemStatus(item.id, 'pending', error.message);
        }

        errorCount++;
      }
    }

    console.log(`[SW] Upload queue processed: ${successCount} success, ${errorCount} errors`);

    // Итоговое уведомление
    if (successCount > 0 && Notification.permission === 'granted') {
      self.registration.showNotification('Фоновая загрузка', {
        body: `Загружено файлов: ${successCount}${errorCount > 0 ? `, ошибок: ${errorCount}` : ''}`,
        icon: '/static/images/icons/icon-192.png',
        tag: 'upload-complete'
      });
    }
  } catch (error) {
    console.error('[SW] Error processing upload queue:', error);
    throw error;
  }
}

console.log('[SW] Service Worker loaded, version:', CACHE_VERSION);
