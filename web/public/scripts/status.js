// No framework here. Fetch /api/status, render the cards, poll for updates.
(function () {
	// Relative path works when the hub serves this page itself. When running
	// `astro dev` on its own port, point at the hub directly.
	const API_BASE = location.port === '4321' ? 'http://localhost:8080' : '';
	const POLL_MS = 10000;
	const MAX_BARS = 50;

	const content = document.getElementById('content');
	const searchInput = document.getElementById('search');
	const sortSelect = document.getElementById('sort');

	let latestData = null;

	function timeAgo(iso) {
		if (!iso) return 'never';
		const diff = Math.max(0, Date.now() - new Date(iso).getTime());
		const s = Math.round(diff / 1000);
		if (s < 60) return `${s} second${s === 1 ? '' : 's'} ago`;
		const m = Math.round(s / 60);
		if (m < 60) return `${m} minute${m === 1 ? '' : 's'} ago`;
		const h = Math.round(m / 60);
		if (h < 24) return `${h} hour${h === 1 ? '' : 's'} ago`;
		const d = Math.round(h / 24);
		return `${d} day${d === 1 ? '' : 's'} ago`;
	}

	function renderBars(history) {
		const bars = new Array(MAX_BARS).fill(null);
		const start = MAX_BARS - history.length;
		history.forEach((p, i) => { bars[start + i] = p.status; });
		return bars
			.map((status) => `<div class="bar ${status || ''}"></div>`)
			.join('');
	}

	function serviceCard(svc) {
		const status = svc.status || 'unknown';
		const latency = svc.latency_ms != null ? `~${svc.latency_ms}ms` : '—';
		const oldest = svc.history && svc.history.length ? svc.history[0].t : null;
		const newest = svc.last_checked;
		return `
			<div class="card" data-name="${svc.name.toLowerCase()}" data-status="${status}" data-uptime="${svc.uptime_pct_90d ?? -1}">
				<div class="card-top">
					<div>
						<h3>${escapeHtml(svc.name)}</h3>
						<p class="target">${escapeHtml(svc.target)}</p>
					</div>
					<span class="pill ${status}">${status.charAt(0).toUpperCase() + status.slice(1)}</span>
				</div>
				<div class="latency-row">${latency}</div>
				<div class="bars">${renderBars(svc.history || [])}</div>
				<div class="time-row">
					<span>${oldest ? timeAgo(oldest) : ''}</span>
					<span>${timeAgo(newest)}</span>
				</div>
			</div>
		`;
	}

	function escapeHtml(s) {
		return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
	}

	function render() {
		if (!latestData) return;
		const search = searchInput.value.trim().toLowerCase();
		const sortBy = sortSelect.value;

		const groups = latestData.groups
			.map((g) => {
				let services = g.services.filter((s) => s.name.toLowerCase().includes(search));
				services = services.slice().sort((a, b) => {
					if (sortBy === 'status') return (a.status || '').localeCompare(b.status || '');
					if (sortBy === 'uptime') return (b.uptime_pct_90d ?? -1) - (a.uptime_pct_90d ?? -1);
					return a.name.localeCompare(b.name);
				});
				return { name: g.name, services };
			})
			.filter((g) => g.services.length > 0);

		if (groups.length === 0) {
			content.innerHTML = '<p class="empty">No endpoints match.</p>';
			return;
		}

		content.innerHTML = groups
			.map(
				(g) => `<div class="grid">${g.services.map(serviceCard).join('')}</div>`
			)
			.join('');
	}

	async function poll() {
		try {
			const res = await fetch(`${API_BASE}/api/status`, { cache: 'no-store' });
			if (!res.ok) throw new Error(`status ${res.status}`);
			latestData = await res.json();
			render();
		} catch (err) {
			console.error('failed to load status:', err);
		}
	}

	searchInput.addEventListener('input', render);
	sortSelect.addEventListener('change', render);

	poll();
	setInterval(poll, POLL_MS);
})();
