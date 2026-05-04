// PhotoCore Common Utilities
// Общие утилиты для всех страниц

// === Favorites ===
window.initFavorites = function(favArray) {
    window.favSet = new Set(favArray || []);
    if (typeof window.updateFavoriteBadges === 'function') {
        window.updateFavoriteBadges();
    }
};

window.updateFavoriteBadges = function() {
    document.querySelectorAll('.media-card').forEach(function(card) {
        const badge = card.querySelector('.favorite-badge');
        if (badge) {
            const isFav = window.favSet && window.favSet.has(card.dataset.id);
            badge.classList.toggle('active', isFav);
            card.dataset.favorite = isFav;
        }
    });
};

// === Formatting Utilities ===
window.formatBytes = function(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
};

window.escapeHtml = function(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
};

window.formatDate = function(dateStr) {
    const date = new Date(dateStr);
    return date.toLocaleDateString('ru-RU', {
        day: '2-digit', month: '2-digit', year: 'numeric',
        hour: '2-digit', minute: '2-digit'
    });
};
