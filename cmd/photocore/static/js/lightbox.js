// PhotoCore Lightbox - Standalone JavaScript Module
// Инициализация глобальных переменных и функций для lightbox компонента

console.log('[Lightbox] Loading lightbox.js module');

// === Global Favorites Set (initialized by each page) ===
window.favSet = window.favSet || new Set();

// === Lightbox State ===
let lbMediaList = [], lbCurrentIndex = 0, lbCurrentMedia = null;
let lbLoadVersion = 0; // Счетчик версий для отмены устаревших загрузок
let lbPreloadCache = new Map(); // Кеш предзагруженных изображений

// === Helper: Format Russian Date ===
function formatRussianDate(dateStr) {
    if (!dateStr) return '';
    const months = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня',
                    'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря2'];
    const date = new Date(dateStr);
    const day = date.getDate();
    const month = months[date.getMonth()];
    const year = date.getFullYear();
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    return day + ' ' + month + ' ' + year + ', ' + hours + ':' + minutes;
}

// === Open Lightbox ===
window.openLightbox = function(id, filename, takenAt, meta) {
    console.log('[Lightbox] Opening:', { id, filename, takenAt, meta });

    // Собираем список всех media-card на странице
    lbMediaList = [];
    document.querySelectorAll('.media-card').forEach((card, index) => {
        const mediaId = card.dataset.id;
        if (mediaId) {
            lbMediaList.push({
                id: mediaId,
                filename: card.dataset.filename || card.querySelector('img')?.alt || '',
                takenAt: card.dataset.takenAt || '',
                meta: card.dataset.meta || ''
            });
            if (mediaId === id) lbCurrentIndex = lbMediaList.length - 1;
        }
    });

    lbShowMedia(id, filename, takenAt, meta);
    document.getElementById('lightbox').classList.add('active');
    document.body.style.overflow = 'hidden';
    lbUpdateNavButtons();
}

// === Show Media in Lightbox ===
function lbShowMedia(id, filename, takenAt, meta) {
    const isFavorite = window.favSet.has(id);
    lbCurrentMedia = { id, filename, takenAt, meta, isFavorite };
    const img = document.getElementById('lightbox-img');
    const loader = document.getElementById('lightbox-loader');

    // Инкрементируем версию загрузки для отмены предыдущих
    lbLoadVersion++;
    const currentLoadVersion = lbLoadVersion;

    console.log('[Lightbox] Loading media:', id, 'version:', currentLoadVersion);

    // Обновляем информацию
    document.getElementById('lightbox-title').textContent = filename;

    // Формируем информацию с датой съемки
    let infoHTML = '<h3>' + filename + '</h3>';
    if (takenAt) {
        const formattedDate = formatRussianDate(takenAt);
        infoHTML += '<div class="meta" style="margin-bottom: 0.5rem; font-size: 0.875rem; opacity: 0.9;">' + formattedDate + '</div>';
    }
    if (meta) {
        infoHTML += '<div class="meta">' + meta + '</div>';
    }
    document.getElementById('lightbox-info').innerHTML = infoHTML;

    const favBtn = document.getElementById('lb-favorite-btn');
    favBtn.querySelector('.icon-star-outline').style.display = isFavorite ? 'none' : 'block';
    favBtn.querySelector('.icon-star-filled').style.display = isFavorite ? 'block' : 'none';
    favBtn.classList.toggle('active', isFavorite);
    document.getElementById('lightbox-counter').textContent = lbMediaList.length > 0 ? (lbCurrentIndex + 1) + ' / ' + lbMediaList.length : '';

    // Проверяем кеш предзагруженных изображений
    const thumbUrl = '/media/' + id + '/thumb/large';
    const fullUrl = '/media/' + id;

    if (lbPreloadCache.has(fullUrl)) {
        // Полное изображение уже загружено - показываем сразу с fade-in
        console.log('[Lightbox] Using cached full image:', id);
        loader.classList.remove('active');
        img.classList.remove('loading');
        img.classList.add('loaded', 'fade-in');
        img.src = fullUrl;

        // Убираем fade-in класс после завершения анимации
        setTimeout(() => img.classList.remove('fade-in'), 200);
    } else {
        // Показываем loader и превью
        loader.classList.add('active');
        img.classList.remove('loaded', 'fade-in');
        img.classList.add('loading');
        img.src = thumbUrl;

        // Начинаем загрузку полного изображения
        const fullImg = new Image();

        fullImg.onload = function() {
            // Проверяем, не устарела ли эта загрузка
            if (currentLoadVersion === lbLoadVersion) {
                console.log('[Lightbox] Full image loaded:', id, 'version:', currentLoadVersion);

                // Скрываем loader, показываем изображение с fade-in эффектом
                loader.classList.remove('active');
                img.classList.remove('loading');
                img.classList.add('loaded', 'fade-in');
                img.src = fullUrl;

                lbPreloadCache.set(fullUrl, true);

                // Убираем fade-in класс после завершения анимации
                setTimeout(() => img.classList.remove('fade-in'), 200);
            } else {
                console.log('[Lightbox] Discarding outdated image:', id, 'version:', currentLoadVersion, 'current:', lbLoadVersion);
            }
        };

        fullImg.onerror = function() {
            // Проверяем актуальность перед обработкой ошибки
            if (currentLoadVersion === lbLoadVersion) {
                console.error('[Lightbox] Failed to load full image:', id);
                // Скрываем loader, оставляем превью
                loader.classList.remove('active');
                img.classList.remove('loading');
                img.classList.add('loaded');
            }
        };

        fullImg.src = fullUrl;
    }

    // Запускаем предзагрузку соседних изображений
    lbPreloadAdjacent();
}

// === Update Navigation Buttons ===
function lbUpdateNavButtons() {
    document.getElementById('lb-prev').disabled = lbCurrentIndex <= 0;
    document.getElementById('lb-next').disabled = lbCurrentIndex >= lbMediaList.length - 1;
}

// === Preload Adjacent Images ===
function lbPreloadAdjacent() {
    // Предзагружаем следующее и предыдущее изображение для плавной навигации
    const indicesToPreload = [];

    // Следующее изображение (приоритет)
    if (lbCurrentIndex + 1 < lbMediaList.length) {
        indicesToPreload.push(lbCurrentIndex + 1);
    }

    // Предыдущее изображение
    if (lbCurrentIndex - 1 >= 0) {
        indicesToPreload.push(lbCurrentIndex - 1);
    }

    // Еще одно вперед (низкий приоритет)
    if (lbCurrentIndex + 2 < lbMediaList.length) {
        indicesToPreload.push(lbCurrentIndex + 2);
    }

    indicesToPreload.forEach(index => {
        const media = lbMediaList[index];
        const fullUrl = '/media/' + media.id;

        // Пропускаем если уже в кеше
        if (lbPreloadCache.has(fullUrl)) {
            return;
        }

        // Запускаем фоновую загрузку
        const preloadImg = new Image();
        preloadImg.onload = function() {
            lbPreloadCache.set(fullUrl, true);
            console.log('[Lightbox] Preloaded:', media.filename);
        };
        preloadImg.onerror = function() {
            console.warn('[Lightbox] Failed to preload:', media.filename);
        };
        preloadImg.src = fullUrl;
    });
}

// === Navigate Lightbox ===
function lbNavigate(direction) {
    const newIndex = lbCurrentIndex + direction;
    if (newIndex < 0 || newIndex >= lbMediaList.length) return;
    lbCurrentIndex = newIndex;
    const media = lbMediaList[lbCurrentIndex];
    lbShowMedia(media.id, media.filename, media.takenAt, media.meta);
    lbUpdateNavButtons();
}

// === Close Lightbox ===
window.closeLightbox = function() {
    document.getElementById('lightbox').classList.remove('active');
    document.body.style.overflow = '';
    lbCurrentMedia = null;

    // Очищаем кеш предзагрузки для экономии памяти
    lbPreloadCache.clear();
    lbLoadVersion = 0;
    console.log('[Lightbox] Closed and cache cleared');
}

// === Keyboard Navigation ===
document.addEventListener('keydown', function(e) {
    if (!document.getElementById('lightbox').classList.contains('active')) return;
    if (e.key === 'Escape') closeLightbox();
    else if (e.key === 'ArrowLeft') { lbNavigate(-1); e.preventDefault(); }
    else if (e.key === 'ArrowRight') { lbNavigate(1); e.preventDefault(); }
});

// === Click outside to close ===
document.addEventListener('click', function(e) {
    if (e.target.id === 'lightbox' || e.target.classList.contains('lightbox-content')) {
        closeLightbox();
    }
});



// === Lightbox Actions ===
function lbToggleFavorite() {
    if (!lbCurrentMedia) return;
    fetch('/api/media/' + lbCurrentMedia.id + '/favorite', { method: 'POST' })
        .then(r => r.json())
        .then(data => {
            // Обновляем глобальный Set
            if (data.is_favorite) window.favSet.add(lbCurrentMedia.id);
            else window.favSet.delete(lbCurrentMedia.id);

            // Обновляем lightbox
            lbCurrentMedia.isFavorite = data.is_favorite;
            const favBtn = document.getElementById('lb-favorite-btn');
            favBtn.querySelector('.icon-star-outline').style.display = data.is_favorite ? 'none' : 'block';
            favBtn.querySelector('.icon-star-filled').style.display = data.is_favorite ? 'block' : 'none';
            favBtn.classList.toggle('active', data.is_favorite);
            if (lbMediaList[lbCurrentIndex]) lbMediaList[lbCurrentIndex].isFavorite = data.is_favorite;

            // Обновляем карточку в сетке
            const card = document.querySelector('.media-card[data-id="' + lbCurrentMedia.id + '"]');
            if (card) {
                card.dataset.favorite = data.is_favorite;
                const badge = card.querySelector('.favorite-badge');
                if (badge) badge.classList.toggle('active', data.is_favorite);
            }
            showToast(data.is_favorite ? 'Добавлено в избранное' : 'Удалено из избранного', 'success');
        })
        .catch(err => showToast('Ошибка: ' + err.message, 'error'));
}

function lbShowAlbumModal() {
    if (!lbCurrentMedia) return;
    fetch('/api/albums').then(r => r.json()).then(albums => {
        const select = document.getElementById('lb-album-select');
        select.innerHTML = '<option value="">Выберите альбом</option>';
        if (albums) albums.forEach(a => select.innerHTML += '<option value="' + a.id + '">' + a.name + '</option>');
        document.getElementById('lb-album-modal').classList.add('active');
    });
}

function lbAddToAlbum() {
    if (!lbCurrentMedia) return;
    const albumId = document.getElementById('lb-album-select').value;
    if (!albumId) { showToast('Выберите альбом', 'warning'); return; }
    fetch('/api/bulk/album', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ media_ids: [lbCurrentMedia.id], album_id: albumId })
    })
    .then(r => r.json())
    .then(() => { showToast('Добавлено в альбом', 'success'); closeModal('lb-album-modal'); })
    .catch(err => showToast('Ошибка: ' + err.message, 'error'));
}

function lbShowTagsModal() {
    if (!lbCurrentMedia) return;
    document.getElementById('lb-tags-input').value = '';
    document.getElementById('lb-tags-modal').classList.add('active');
}

function lbAddTags() {
    if (!lbCurrentMedia) return;
    const tags = document.getElementById('lb-tags-input').value.split(',').map(t => t.trim()).filter(t => t);
    if (tags.length === 0) { showToast('Введите хотя бы один тег', 'warning'); return; }
    fetch('/api/bulk/tags', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ media_ids: [lbCurrentMedia.id], tags: tags })
    })
    .then(r => r.json())
    .then(() => { showToast('Теги добавлены', 'success'); closeModal('lb-tags-modal'); })
    .catch(err => showToast('Ошибка: ' + err.message, 'error'));
}

function lbDownload() {
    if (!lbCurrentMedia) return;
    const a = document.createElement('a');
    a.href = '/media/' + lbCurrentMedia.id;
    a.download = lbCurrentMedia.filename;
    document.body.appendChild(a); a.click(); a.remove();
    showToast('Скачивание: ' + lbCurrentMedia.filename, 'info');
}

function lbDelete() {
    if (!lbCurrentMedia) return;
    if (!confirm('Удалить "' + lbCurrentMedia.filename + '" из базы данных?')) return;
    const deletedId = lbCurrentMedia.id;
    fetch('/api/bulk/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ media_ids: [deletedId] })
    })
    .then(r => r.json())
    .then(() => {
        showToast('Удалено', 'success');
        lbMediaList.splice(lbCurrentIndex, 1);
        const card = document.querySelector('.media-card[data-id="' + deletedId + '"]');
        if (card) card.remove();
        if (lbMediaList.length === 0) closeLightbox();
        else {
            if (lbCurrentIndex >= lbMediaList.length) lbCurrentIndex = lbMediaList.length - 1;
            const media = lbMediaList[lbCurrentIndex];
            lbShowMedia(media.id, media.filename, media.takenAt, media.meta);
            lbUpdateNavButtons();
        }
    })
    .catch(err => showToast('Ошибка: ' + err.message, 'error'));
}

// === Global Toggle Favorite (для карточек) ===
window.toggleFavorite = function(id, event) {
    event.stopPropagation();
    fetch('/api/media/' + id + '/favorite', { method: 'POST' })
        .then(r => r.json())
        .then(data => {
            // Обновляем глобальный Set
            if (data.is_favorite) window.favSet.add(id);
            else window.favSet.delete(id);

            // Обновляем UI - ищем badge через closest так как event.target может быть svg/path
            const badge = event.target.closest('.favorite-badge');
            if (badge) badge.classList.toggle('active', data.is_favorite);
            const card = event.target.closest('.media-card');
            if (card) card.dataset.favorite = data.is_favorite;
        })
        .catch(err => showToast('Ошибка: ' + err.message, 'error'));
}

// Confirm all functions loaded
console.log('[Lightbox] All functions loaded:', {
    openLightbox: typeof window.openLightbox,
    closeLightbox: typeof window.closeLightbox,
    toggleFavorite: typeof window.toggleFavorite
});
