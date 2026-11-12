/* EdgeStack presentation overlay for the pinned Portainer CE 2.45.3 UI.
 * This file only edits fixed display copy, accessibility labels and branding.
 * It never reads credentials, browser storage, form values or API responses.
 * Copyright and license notices in the upstream application are retained.
 */
(function () {
  'use strict';

  const VERSION = '__EDGESTACK_VERSION__';
  const COPY = Object.freeze(__EDGESTACK_TRANSLATIONS__);
  const script = document.currentScript;
  const assetBase = new URL('.', script.src);
  const original = new WeakMap();
  const attributes = ['title', 'aria-label', 'placeholder', 'alt'];
  const excluded = [
    'script', 'style', 'noscript', 'pre', 'code', 'textarea', 'input',
    '[contenteditable="true"]', '[data-edgestack-raw]',
    '.cm-editor', '.CodeMirror', '.monaco-editor', '.ace_editor', '.xterm',
    '.log_viewer', '.log-viewer-output',
  ].join(',');
  const statuses = new Set([
    'running', 'stopped', 'healthy', 'unhealthy', 'starting', 'created',
    'exited', 'paused', 'restarting', 'dead', 'removing', 'running (healthy)',
    'Up', 'Down', 'Heartbeat', 'Running', 'Stopped', 'Healthy', 'Unhealthy',
    'Active', 'Inactive', 'Enabled', 'Disabled', 'Not available', 'None',
    'Yes', 'No', 'Public', 'Private', 'Limited', 'Pending', 'Success', 'Failed',
    'administrators', 'public', 'private', 'Used', 'Unused', 'System', 'in use',
    'Static', 'Dynamic',
  ]);
  const dataColumns = new Set([
    'Name', 'Image', 'ID', 'Id', 'IP Address', 'IP address', 'IPv4 Address',
    'IPv6 Address', 'Published Ports', 'Stack', 'Node', 'Driver', 'Mountpoint',
    'Path', 'Address', 'URL', 'Key', 'Value', 'Labels', 'Owner', 'Group',
    'Tags', 'Endpoint', 'Environment', 'Username', 'User', 'Repository', 'Tag',
  ]);

  function normalize(value) {
    return value.replace(/\s+/g, ' ').trim();
  }

  function translate(value) {
    const key = normalize(value);
    let result = COPY[key];
    if (!result && /[:：]$/.test(key)) {
      const label = COPY[key.slice(0, -1)];
      if (label) result = label + '：';
    }
    // Explicit UI counters/time strings only; never translate arbitrary prose.
    if (!result) {
      const rules = [
        [/^(\d+) containers?$/i, '$1 个容器'],
        [/^(\d+) images?$/i, '$1 个镜像'],
        [/^(\d+) volumes?$/i, '$1 个数据卷'],
        [/^(\d+) networks?$/i, '$1 个网络'],
        [/^(\d+) environments?$/i, '$1 个环境'],
        [/^(\d+) items?$/i, '$1 项'],
        [/^(\d+) selected$/i, '已选择 $1 项'],
        [/^(\d+) seconds? ago$/, '$1 秒前'],
        [/^(\d+) minutes? ago$/, '$1 分钟前'],
        [/^(\d+) hours? ago$/, '$1 小时前'],
        [/^(\d+) days? ago$/, '$1 天前'],
        [/^a few seconds ago$/, '几秒前'],
        [/^a minute ago$/, '1 分钟前'],
        [/^an hour ago$/, '1 小时前'],
        [/^a day ago$/, '1 天前'],
        [/^(\d+) of (\d+) containers running$/, '$2 个容器中有 $1 个运行中'],
        [/^Filter by (Total|Up|Down|Outdated|Unassigned)$/, (_, state) => '按' + (COPY[state] || state) + '筛选'],
        [/^Items per page:?$/, '每页条数：'],
        [/^Showing (\d+) to (\d+) of (\d+) (?:items|entries)$/, '显示第 $1–$2 项，共 $3 项'],
      ];
      for (const [pattern, replacement] of rules) {
        if (pattern.test(key)) {
          result = key.replace(pattern, replacement);
          break;
        }
      }
    }
    return result ? value.replace(/\S(?:[\s\S]*\S)?/, () => result) : value;
  }

  function sourceText(element) {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    let result = '';
    while (walker.nextNode()) {
      const node = walker.currentNode;
      const previous = original.get(node);
      result += previous && previous.translated === node.nodeValue
        ? previous.source : node.nodeValue;
    }
    return normalize(result);
  }

  function isResourceValue(element, value) {
    if (element.closest('[data-cy^="environment-card-"] .break-words.font-bold')) return true;
    if (element.closest('#sideview .whitespace-nowrap.text-white')) return true;
    if (element.closest('[data-cy="username"], [data-cy="resource-name"], [data-edgestack-raw]')) return true;
    const action = element.closest('button, [role="button"], label, th, [role="status"], [role="tooltip"], .tooltip, .tippy-content');
    if (action) return false;
    const link = element.closest('a[title]');
    if (link && normalize(link.getAttribute('title')) === normalize(value) && !link.closest('#sideview')) return true;
    const cell = element.closest('td');
    if (!cell) return false;
    if (cell.colSpan > 1) return false;
    const table = cell.closest('table');
    const headers = table && table.tHead && table.tHead.rows[0];
    if (headers && headers.cells[cell.cellIndex]) {
      const heading = sourceText(headers.cells[cell.cellIndex]);
      if (heading === 'Ownership') return !statuses.has(normalize(value));
      if (dataColumns.has(heading)) return true;
      if (/^(?:Status|State|Health|Type|Access control)$/.test(heading)) return false;
      // Table data is protected unless it is a recognized status or UI control.
      return !statuses.has(normalize(value)) && !cell.classList.contains('text-center');
    }
    if (cell.cellIndex > 0) return !statuses.has(normalize(value));
    // Key/value and host detail tables put their fixed labels in the first cell.
    return false;
  }

  function localizeText(node) {
    const element = node.parentElement;
    if (!element || element.closest(excluded) || !/[A-Za-z]/.test(node.nodeValue)) return;
    if (isResourceValue(element, node.nodeValue)) return;
    // React emits these counter fragments as separate text nodes.
    const counterOf = normalize(node.nodeValue) === 'of' && /containers running/.test(sourceText(element));
    const result = counterOf ? node.nodeValue.replace('of', '/') : translate(node.nodeValue);
    if (result !== node.nodeValue) {
      original.set(node, { source: node.nodeValue, translated: result });
      node.nodeValue = result;
    }
  }

  function brand(element) {
    if (element.tagName !== 'IMG') return;
    const home = element.closest('[data-cy="portainerSidebar-homeImage"]');
    if (!home && !element.classList.contains('simple-box-logo')) return;
    const collapsed = home && element.className.includes('!max-h-[27px]');
    const target = new URL(collapsed ? 'edgestack-icon.svg' : 'edgestack-logo.svg', assetBase).href;
    if (element.src !== target) element.src = target;
    if (element.alt !== 'EdgeStack.io') element.alt = 'EdgeStack.io';
    if (!element.classList.contains('edgestack-brand-logo')) element.classList.add('edgestack-brand-logo');
  }

  function localizeAttributes(element) {
    if (element.closest(excluded) && !element.matches('input, textarea')) return;
    for (const attribute of attributes) {
      if (!element.hasAttribute(attribute)) continue;
      const value = element.getAttribute(attribute);
      // A resource's title often repeats its name; keep it verbatim.
      if (attribute !== 'placeholder' && normalize(value) === sourceText(element) && isResourceValue(element, value)) continue;
      const result = translate(value);
      if (result !== value) element.setAttribute(attribute, result);
    }
    brand(element);
  }

  function scan(root) {
    if (root.nodeType === Node.TEXT_NODE) return localizeText(root);
    if (root.nodeType !== Node.ELEMENT_NODE && root.nodeType !== Node.DOCUMENT_NODE) return;
    if (root.nodeType === Node.ELEMENT_NODE) localizeAttributes(root);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        if (node.nodeType === Node.ELEMENT_NODE && node.matches(excluded)) {
          localizeAttributes(node);
          return NodeFilter.FILTER_REJECT;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    while (walker.nextNode()) {
      const node = walker.currentNode;
      if (node.nodeType === Node.TEXT_NODE) localizeText(node);
      else localizeAttributes(node);
    }
  }

  function brandTitle() {
    const title = document.title;
    if (!title.startsWith('EdgeStack.io')) {
      const clean = title.replace(/Portainer(?:\.io)?/gi, '').trim();
      document.title = 'EdgeStack.io' + (clean ? ' · ' + clean : '');
    }
  }

  const pending = new Set();
  let scheduled = false;
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === 'childList') {
        for (const node of record.addedNodes) pending.add(node);
      } else pending.add(record.target);
    }
    if (!scheduled) {
      scheduled = true;
      requestAnimationFrame(flush);
    }
  });
  const observerOptions = {
    subtree: true, childList: true, characterData: true, attributes: true,
    attributeFilter: [...attributes, 'src'],
  };

  function flush() {
    observer.disconnect();
    scheduled = false;
    for (const node of pending) {
      if (node.isConnected) scan(node);
    }
    pending.clear();
    brandTitle();
    observer.observe(document.documentElement, observerOptions);
  }

  function start() {
    document.documentElement.lang = 'zh-CN';
    pending.add(document.body);
    flush();
  }

  window.EdgeStackUI = Object.freeze({
    version: VERSION,
    locale: 'zh-CN',
    phraseCount: Object.keys(COPY).length,
    refresh() { pending.add(document.body); flush(); },
  });
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
  else start();
})();
