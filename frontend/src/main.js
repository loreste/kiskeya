import './style.css';
import './app.css';

import * as App from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

// DOM Elements cache
const appContainer = document.querySelector('#app');

// State tracking
let activeTab = 'dialer';
let callTimerInterval = null;
let callDurationSec = 0;
let registrationState = 'idle';
let currentCallState = 'idle';
// In-memory mirror of the saved SIP profile (non-secret fields used for display).
let appConfig = {};

// HTML Skeleton structure
appContainer.innerHTML = `
  <!-- Splash Screen Overlay -->
  <div class="splash-screen" id="splash-screen">
    <div class="splash-content">
      <div class="splash-logo-container">
        <div class="splash-logo">
          <svg class="dynamic-logo-svg" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
            <defs>
              <linearGradient id="logoGradSplash" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stop-color="var(--accent-start)" />
                <stop offset="50%" stop-color="var(--accent-mid)" />
                <stop offset="100%" stop-color="var(--accent-end)" />
              </linearGradient>
            </defs>
            <path class="logo-phone-spine" d="M30,20 C30,20 22,35 22,50 C22,65 30,80 30,80 C32,83 28,87 25,87 C20,80 15,65 15,50 C15,35 20,20 25,13 C28,13 32,17 30,20 Z" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-1" x="42" y="45" width="6" height="15" rx="3" transform="rotate(-45 42 45)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-2" x="52" y="35" width="6" height="22" rx="3" transform="rotate(-45 52 35)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-3" x="62" y="25" width="6" height="30" rx="3" transform="rotate(-45 62 25)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-4" x="72" y="15" width="6" height="38" rx="3" transform="rotate(-45 72 15)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-5" x="42" y="55" width="6" height="15" rx="3" transform="rotate(45 42 55)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-6" x="52" y="65" width="6" height="22" rx="3" transform="rotate(45 52 65)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-7" x="62" y="75" width="6" height="30" rx="3" transform="rotate(45 62 75)" fill="url(#logoGradSplash)"/>
            <rect class="wave-bar bar-8" x="72" y="85" width="6" height="38" rx="3" transform="rotate(45 72 85)" fill="url(#logoGradSplash)"/>
          </svg>
        </div>
        <div class="splash-ring"></div>
        <div class="splash-ring-2"></div>
      </div>
      <h1 class="splash-title">KISKEYA</h1>
      <div class="splash-subtitle">Premium SIP Softphone</div>
      <div class="splash-loader-bar">
        <div class="splash-loader-fill"></div>
      </div>
      <div class="splash-status" id="splash-status-text">Initializing SIP Engine...</div>
    </div>
  </div>

  <div class="app-container">
    <!-- Sidebar Panel -->
    <div class="sidebar">
      <div>
        <div class="logo-section">
          <div class="logo-icon">
            <svg class="dynamic-logo-svg" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
              <defs>
                <linearGradient id="logoGradSidebar" x1="0%" y1="0%" x2="100%" y2="100%">
                  <stop offset="0%" stop-color="var(--accent-start)" />
                  <stop offset="50%" stop-color="var(--accent-mid)" />
                  <stop offset="100%" stop-color="var(--accent-end)" />
                </linearGradient>
              </defs>
              <path class="logo-phone-spine" d="M30,20 C30,20 22,35 22,50 C22,65 30,80 30,80 C32,83 28,87 25,87 C20,80 15,65 15,50 C15,35 20,20 25,13 C28,13 32,17 30,20 Z" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-1" x="42" y="45" width="6" height="15" rx="3" transform="rotate(-45 42 45)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-2" x="52" y="35" width="6" height="22" rx="3" transform="rotate(-45 52 35)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-3" x="62" y="25" width="6" height="30" rx="3" transform="rotate(-45 62 25)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-4" x="72" y="15" width="6" height="38" rx="3" transform="rotate(-45 72 15)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-5" x="42" y="55" width="6" height="15" rx="3" transform="rotate(45 42 55)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-6" x="52" y="65" width="6" height="22" rx="3" transform="rotate(45 52 65)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-7" x="62" y="75" width="6" height="30" rx="3" transform="rotate(45 62 75)" fill="url(#logoGradSidebar)"/>
              <rect class="wave-bar bar-8" x="72" y="85" width="6" height="38" rx="3" transform="rotate(45 72 85)" fill="url(#logoGradSidebar)"/>
            </svg>
          </div>
          <div class="logo-text">
            <h1>Kiskeya</h1>
            <span>SIP softphone</span>
          </div>
        </div>

        <div class="profile-card">
          <div class="profile-avatar">K</div>
          <div class="profile-info">
            <div class="profile-name" id="sidebar-profile-name">Not Registered</div>
            <div class="profile-status">
              <span class="status-dot idle" id="sidebar-status-dot" aria-hidden="true"></span>
              <span id="sidebar-status-text" aria-live="polite">Disconnected</span>
            </div>
          </div>
        </div>

        <div class="nav-menu" role="tablist" aria-label="Main navigation" aria-orientation="vertical">
          <button type="button" class="nav-item active" data-tab="dialer" role="tab" aria-selected="true" aria-controls="panel-dialer" id="tab-dialer"><span class="nav-icon" aria-hidden="true">☎</span><span>Dialpad</span></button>
          <button type="button" class="nav-item" data-tab="contacts" role="tab" aria-selected="false" aria-controls="panel-contacts" id="tab-contacts" tabindex="-1"><span class="nav-icon" aria-hidden="true">☷</span><span>Contacts</span></button>
          <button type="button" class="nav-item" data-tab="history" role="tab" aria-selected="false" aria-controls="panel-history" id="tab-history" tabindex="-1"><span class="nav-icon" aria-hidden="true">↺</span><span>History</span></button>
          <button type="button" class="nav-item" data-tab="settings" role="tab" aria-selected="false" aria-controls="panel-settings" id="tab-settings" tabindex="-1"><span class="nav-icon" aria-hidden="true">⚙</span><span>Settings</span></button>
          <button type="button" class="nav-item" data-tab="diagnostics" role="tab" aria-selected="false" aria-controls="panel-diagnostics" id="tab-diagnostics" tabindex="-1"><span class="nav-icon" aria-hidden="true">⌁</span><span>Diagnostics</span></button>
        </div>
      </div>
      <div class="sidebar-footer">
        Kiskeya v1.0.0
      </div>
    </div>

    <!-- Main Content Panel -->
    <div class="main-content">
      <div class="app-topbar">
        <div>
          <div class="topbar-title">Softphone</div>
          <div class="topbar-subtitle">Ready for SIP calls and extension dialing</div>
        </div>
        <div class="topbar-presence">
          <span class="status-dot idle" id="topbar-status-dot" aria-hidden="true"></span>
          <span id="topbar-status-text" aria-live="polite">Offline</span>
        </div>
      </div>
      
      <!-- Tab 1: Dialer -->
      <div class="tab-panel active" id="panel-dialer" role="tabpanel" aria-labelledby="tab-dialer">
        <div class="dialer-grid">
          <!-- Keypad & Screen -->
          <div class="phone-card">
            <div class="screen-container">
              <div class="screen-meta-row">
                <div class="screen-call-state" id="call-state-label" aria-live="polite">Idle</div>
                <div class="screen-timer" id="call-timer-display" style="display: none;">00:00</div>
              </div>
              <input type="text" class="dial-input" id="dial-display" placeholder="Type number or SIP address" autocomplete="off" />
              <div class="screen-hint">Use digits, +, *, #, or paste a SIP URI</div>
            </div>
            
            <div class="keypad">
              <button type="button" class="key" data-val="1" aria-label="1"><span class="key-number" aria-hidden="true">1</span><span class="key-letters" aria-hidden="true"></span></button>
              <button type="button" class="key" data-val="2" aria-label="2 A B C"><span class="key-number" aria-hidden="true">2</span><span class="key-letters" aria-hidden="true">abc</span></button>
              <button type="button" class="key" data-val="3" aria-label="3 D E F"><span class="key-number" aria-hidden="true">3</span><span class="key-letters" aria-hidden="true">def</span></button>
              <button type="button" class="key" data-val="4" aria-label="4 G H I"><span class="key-number" aria-hidden="true">4</span><span class="key-letters" aria-hidden="true">ghi</span></button>
              <button type="button" class="key" data-val="5" aria-label="5 J K L"><span class="key-number" aria-hidden="true">5</span><span class="key-letters" aria-hidden="true">jkl</span></button>
              <button type="button" class="key" data-val="6" aria-label="6 M N O"><span class="key-number" aria-hidden="true">6</span><span class="key-letters" aria-hidden="true">mno</span></button>
              <button type="button" class="key" data-val="7" aria-label="7 P Q R S"><span class="key-number" aria-hidden="true">7</span><span class="key-letters" aria-hidden="true">pqrs</span></button>
              <button type="button" class="key" data-val="8" aria-label="8 T U V"><span class="key-number" aria-hidden="true">8</span><span class="key-letters" aria-hidden="true">tuv</span></button>
              <button type="button" class="key" data-val="9" aria-label="9 W X Y Z"><span class="key-number" aria-hidden="true">9</span><span class="key-letters" aria-hidden="true">wxyz</span></button>
              <button type="button" class="key" data-val="*" aria-label="Star"><span class="key-number" aria-hidden="true">*</span><span class="key-letters" aria-hidden="true"></span></button>
              <button type="button" class="key" data-val="0" aria-label="0 plus"><span class="key-number" aria-hidden="true">0</span><span class="key-letters" aria-hidden="true">+</span></button>
              <button type="button" class="key" data-val="#" aria-label="Pound"><span class="key-number" aria-hidden="true">#</span><span class="key-letters" aria-hidden="true"></span></button>
            </div>

            <div class="call-actions">
              <button type="button" class="call-btn dial" id="btn-dial" title="Start call">Call</button>
              <button type="button" class="call-btn hangup" id="btn-hangup" style="display: none;" title="End call">End</button>
              <button type="button" class="key utility-key" id="btn-backspace" title="Backspace" aria-label="Backspace">⌫</button>
            </div>
          </div>

          <!-- Active Call & Audio Console -->
          <div class="call-controls">
            <div class="controls-card">
              <h3>Call Controls</h3>
              <div class="control-buttons-row">
                <button class="control-action-btn" id="btn-mute" disabled>
                  <span class="control-icon">Mic</span>
                  <span>Mute</span>
                </button>
                <button class="control-action-btn" id="btn-hold" disabled aria-disabled="true" title="Hold — coming soon">
                  <span class="control-icon">Ⅱ</span>
                  <span>Hold</span>
                </button>
                <button class="control-action-btn" id="btn-transfer" disabled aria-disabled="true" title="Transfer — coming soon">
                  <span class="control-icon">⇄</span>
                  <span>Transfer</span>
                </button>
              </div>
            </div>

            <div class="controls-card">
              <h3>Audio</h3>
              <div class="visualizer-section">
                <div class="level-row">
                  <div class="level-label">
                    <span>Microphone Level</span>
                    <span id="val-mic-level">0%</span>
                  </div>
                  <div class="level-bar" id="bar-mic-level" role="progressbar" aria-label="Microphone level" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0">
                    <div class="level-fill mic" id="fill-mic-level"></div>
                  </div>
                </div>

                <div class="level-row">
                  <div class="level-label">
                    <span>Speaker Level</span>
                    <span id="val-speaker-level">0%</span>
                  </div>
                  <div class="level-bar" id="bar-speaker-level" role="progressbar" aria-label="Speaker level" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0">
                    <div class="level-fill speaker" id="fill-speaker-level"></div>
                  </div>
                </div>
              </div>
            </div>
            
            <div class="controls-card codec-card">
              <span>Active codec</span>
              <strong id="val-active-codec">None</strong>
            </div>
            <div class="controls-card codec-card">
              <span>Media security</span>
              <strong id="val-media-security" aria-live="polite">—</strong>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab 2: Contacts -->
      <div class="tab-panel" id="panel-contacts">
        <div class="dashboard-header">
          <h2 class="dashboard-title">Contact Directory</h2>
          <p class="dashboard-desc">Access speed dial or create new contacts.</p>
        </div>

        <div class="list-layout">
          <!-- Contact List Card -->
          <div class="scroll-list-card">
            <div class="search-bar-row">
              <input type="text" class="form-input" id="contact-search" placeholder="Search contacts by name..." />
            </div>
            <div class="list-scroll-area" id="contacts-list-container">
              <!-- Contacts load here -->
            </div>
          </div>

          <!-- Add Contact Form -->
          <div class="status-widget">
            <h3>Add New Contact</h3>
            <div class="form-group">
              <label class="form-label" for="new-contact-name">Full Name</label>
              <input type="text" class="form-input" id="new-contact-name" placeholder="e.g. John Doe" aria-describedby="new-contact-name-error" />
              <div class="field-error" id="new-contact-name-error" aria-live="polite"></div>
            </div>
            <div class="form-group">
              <label class="form-label" for="new-contact-uri">SIP URI / Address</label>
              <input type="text" class="form-input" id="new-contact-uri" placeholder="e.g. 100@sip.example.com" aria-describedby="new-contact-uri-error" />
              <div class="field-error" id="new-contact-uri-error" aria-live="polite"></div>
            </div>
            <button type="button" class="primary-btn" id="btn-save-contact" style="width: 100%;">Save Contact</button>
          </div>
        </div>
      </div>

      <!-- Tab 3: Call History -->
      <div class="tab-panel" id="panel-history">
        <div class="dashboard-header" style="display: flex; justify-content: space-between; align-items: center;">
          <div>
            <h2 class="dashboard-title">Call History Logs</h2>
            <p class="dashboard-desc">Recent incoming and outgoing calls.</p>
          </div>
          <button type="button" class="secondary-btn" id="btn-clear-history">Clear Logs</button>
        </div>

        <div class="scroll-list-card" style="height: 520px;">
          <div class="list-scroll-area" id="history-list-container">
            <!-- Call history logs load here -->
          </div>
        </div>
      </div>

      <!-- Tab 4: Settings -->
      <div class="tab-panel" id="panel-settings">
        <div class="dashboard-header">
          <h2 class="dashboard-title">Kiskeya Configuration</h2>
          <p class="dashboard-desc">Manage SIP profiles and local hardware audio configurations.</p>
        </div>

        <div class="settings-grid">
          <!-- SIP Account Credentials -->
          <div class="settings-form-card">
            <div class="form-group">
              <label class="form-label">Display Name</label>
              <input type="text" class="form-input" id="cfg-display-name" placeholder="e.g. Alice Office" />
            </div>
            <div class="form-group row">
              <div>
                <label class="form-label" for="cfg-username">SIP Username (Extension)</label>
                <input type="text" class="form-input" id="cfg-username" placeholder="e.g. 1001" aria-describedby="cfg-username-error" />
                <div class="field-error" id="cfg-username-error" aria-live="polite"></div>
              </div>
              <div>
                <label class="form-label">Secret Password</label>
                <input type="password" class="form-input" id="cfg-password" placeholder="••••••••" />
              </div>
            </div>
            <div class="form-group">
              <label class="form-label" for="cfg-domain">SIP Domain / Registrar Server</label>
              <input type="text" class="form-input" id="cfg-domain" placeholder="e.g. sip.example.com" aria-describedby="cfg-domain-error" />
              <div class="field-error" id="cfg-domain-error" aria-live="polite"></div>
            </div>
            <div class="form-group row">
              <div>
                <label class="form-label">Outbound Proxy (Optional)</label>
                <input type="text" class="form-input" id="cfg-proxy" placeholder="e.g. proxy.example.com:5060" />
              </div>
              <div>
                <label class="form-label">Transport Protocol</label>
                <select class="form-select" id="cfg-protocol">
                  <option value="udp">UDP (Recommended)</option>
                  <option value="tcp">TCP</option>
                  <option value="tls">TLS (Encrypted)</option>
                </select>
              </div>
            </div>
            <div class="form-group">
              <label class="form-label">STUN Server (NAT Traversal)</label>
              <input type="text" class="form-input" id="cfg-stun" placeholder="e.g. stun.l.google.com:19302 (Leave empty to disable)" />
            </div>
            <div class="form-group">
              <label class="form-checkbox">
                <input type="checkbox" id="cfg-media-encryption" />
                <span>Encrypt media (SRTP) — offered on outgoing calls; incoming calls auto-match the caller</span>
              </label>
              <label class="form-checkbox">
                <input type="checkbox" id="cfg-insecure-tls" />
                <span>Allow self-signed / unverified TLS certificates (TLS transport only)</span>
              </label>
            </div>
            <div class="form-actions">
              <button type="button" class="primary-btn" id="btn-save-settings">Save & Register</button>
              <button type="button" class="secondary-btn" id="btn-unregister">Unregister</button>
            </div>
          </div>

          <!-- Registration Status & Audio Devices Config -->
          <div class="status-widget">
            <div class="status-badge-card idle" id="settings-status-card">
              <div class="status-badge-title">SIP Registration</div>
              <div class="status-badge-value" id="settings-status-value" aria-live="polite">Unregistered</div>
              <div class="status-error-msg" id="settings-status-error" style="display: none;" aria-live="polite"></div>
            </div>

            <div>
              <div class="section-title-row">
                <h3 class="panel-kicker">Audio Devices</h3>
                <button type="button" class="secondary-btn" id="btn-refresh-audio" style="font-size: 11px; padding: 4px 10px;">Refresh</button>
              </div>
              <div class="form-group">
                <label class="form-label">Input (Microphone)</label>
                <select class="form-select" id="select-mic">
                  <option value="default">Default Microphone</option>
                </select>
              </div>
              <div class="form-group">
                <label class="form-label">Output (Speaker)</label>
                <select class="form-select" id="select-speaker">
                  <option value="default">Default Speaker</option>
                </select>
              </div>

              <div class="theme-section">
                <h3 class="panel-kicker">Visual Theme</h3>
                <div class="form-group">
                  <label class="form-label">Color Scheme</label>
                  <select class="form-select" id="select-theme">
                    <option value="default">Neon Eclipse (Purple/Cyan)</option>
                    <option value="sunset">Sunset Copper (Orange/Red)</option>
                    <option value="emerald">Emerald Aurora (Mint/Green)</option>
                    <option value="ocean">Oceanic Cobalt (Blue/Ice)</option>
                  </select>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab 5: Diagnostics Console -->
      <div class="tab-panel" id="panel-diagnostics">
        <div class="dashboard-header">
          <h2 class="dashboard-title">SIP Debugging Terminal</h2>
          <p class="dashboard-desc">Real-time raw SIP network traffic logs.</p>
        </div>

        <div class="diagnostics-container">
          <div class="diagnostics-controls">
            <input type="text" class="form-input" id="diagnostics-filter" placeholder="Filter logs (e.g. INVITE)..." style="width: 280px;" />
            <button type="button" class="secondary-btn" id="btn-clear-logs">Clear Console</button>
          </div>
          <div class="diagnostics-console" id="console-log">Ready for SIP registration trace...
----------------------------------------
</div>
        </div>
      </div>

    </div>
  </div>

  <!-- Modal Overlay: Incoming Call -->
  <div class="incoming-call-overlay" id="incoming-call-modal" style="display: none;" role="dialog" aria-modal="true" aria-labelledby="incoming-caller-id" aria-describedby="incoming-call-subtitle">
    <div class="incoming-call-card">
      <div class="incoming-call-avatar" aria-hidden="true">📞</div>
      <div class="incoming-caller-name" id="incoming-caller-id">Unknown Caller</div>
      <div class="incoming-caller-subtitle" id="incoming-call-subtitle">Incoming SIP Call...</div>
      <div class="incoming-actions-row">
        <button type="button" class="incoming-action-btn accept" id="btn-incoming-accept" aria-label="Accept call">📞</button>
        <button type="button" class="incoming-action-btn decline" id="btn-incoming-decline" aria-label="Decline call">❌</button>
      </div>
    </div>
  </div>
`;

// --- Splash Screen Fade Out ---
const splashScreen = document.getElementById('splash-screen');
const splashStatus = document.getElementById('splash-status-text');

const steps = [
  { time: 400, text: "Loading audio drivers..." },
  { time: 900, text: "Initializing SIP registration client..." },
  { time: 1400, text: "Searching local audio hardware..." },
  { time: 1800, text: "Ready!" }
];

steps.forEach(step => {
  setTimeout(() => {
    if (splashStatus) splashStatus.innerText = step.text;
  }, step.time);
});

setTimeout(() => {
  if (splashScreen) {
    splashScreen.classList.add('fade-out');
    setTimeout(() => {
      splashScreen.remove();
    }, 800);
  }
}, 2400);

// --- UI Navigation Logic (ARIA tablist) ---
const navItems = Array.from(document.querySelectorAll('.nav-menu .nav-item'));

function activateTab(item, { focus = false } = {}) {
  const tabName = item.getAttribute('data-tab');

  // Deactivate current nav and panel
  const prevNav = document.querySelector('.nav-item.active');
  const prevPanel = document.querySelector('.tab-panel.active');
  if (prevNav) {
    prevNav.classList.remove('active');
    prevNav.setAttribute('aria-selected', 'false');
    prevNav.setAttribute('tabindex', '-1');
  }
  if (prevPanel) prevPanel.classList.remove('active');

  // Activate new
  item.classList.add('active');
  item.setAttribute('aria-selected', 'true');
  item.setAttribute('tabindex', '0');
  const panel = document.querySelector(`#panel-${tabName}`);
  if (panel) panel.classList.add('active');
  activeTab = tabName;

  if (focus) item.focus();
}

navItems.forEach((item, idx) => {
  item.addEventListener('click', () => activateTab(item));

  // Roving-tabindex arrow key navigation per ARIA tablist pattern.
  item.addEventListener('keydown', (event) => {
    let targetIdx = null;
    if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
      targetIdx = (idx + 1) % navItems.length;
    } else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
      targetIdx = (idx - 1 + navItems.length) % navItems.length;
    } else if (event.key === 'Home') {
      targetIdx = 0;
    } else if (event.key === 'End') {
      targetIdx = navItems.length - 1;
    }
    if (targetIdx !== null) {
      event.preventDefault();
      activateTab(navItems[targetIdx], { focus: true });
    }
  });
});

// --- Settings Profile Persistence (OS keychain via Go backend) ---
async function loadAccountSettings() {
  let cfg = {};
  try {
    cfg = await App.LoadAccount() || {};
  } catch (e) {
    console.error("Failed to load account from backend:", e);
  }
  appConfig = cfg;

  if (cfg.username) {
    document.getElementById('cfg-display-name').value = cfg.displayName || '';
    document.getElementById('cfg-username').value = cfg.username || '';
    document.getElementById('cfg-password').value = cfg.password || '';
    document.getElementById('cfg-domain').value = cfg.domain || '';
    document.getElementById('cfg-proxy').value = cfg.proxy || '';
    document.getElementById('cfg-protocol').value = cfg.protocol || 'udp';
    document.getElementById('cfg-stun').value = cfg.stunServer || '';
    document.getElementById('cfg-media-encryption').checked = !!cfg.mediaEncryption;
    document.getElementById('cfg-insecure-tls').checked = !!cfg.allowInsecureTLS;

    // Reflect loaded transport in the insecure-TLS enable/disable state.
    if (typeof syncInsecureTLSState === 'function') syncInsecureTLSState();

    // Auto-register on launch if credentials exist
    setTimeout(() => {
      saveAndRegister();
    }, 1000);
  }
}

// --- Inline field validation helpers ---
function setFieldError(inputId, errorId, message) {
  const input = document.getElementById(inputId);
  const errEl = document.getElementById(errorId);
  if (input) {
    input.classList.add('input-error');
    input.setAttribute('aria-invalid', 'true');
  }
  if (errEl) {
    errEl.innerText = message;
    errEl.classList.add('visible');
  }
}

function clearFieldError(inputId, errorId) {
  const input = document.getElementById(inputId);
  const errEl = document.getElementById(errorId);
  if (input) {
    input.classList.remove('input-error');
    input.removeAttribute('aria-invalid');
  }
  if (errEl) {
    errEl.innerText = '';
    errEl.classList.remove('visible');
  }
}

async function saveAndRegister() {
  const displayName = document.getElementById('cfg-display-name').value.trim();
  const username = document.getElementById('cfg-username').value.trim();
  const password = document.getElementById('cfg-password').value.trim();
  const domain = document.getElementById('cfg-domain').value.trim();
  const proxy = document.getElementById('cfg-proxy').value.trim();
  const protocol = document.getElementById('cfg-protocol').value;
  const stunServer = document.getElementById('cfg-stun').value.trim();
  const mediaEncryption = document.getElementById('cfg-media-encryption').checked;
  const allowInsecureTLS = document.getElementById('cfg-insecure-tls').checked;

  // Inline validation (no blocking alert)
  clearFieldError('cfg-username', 'cfg-username-error');
  clearFieldError('cfg-domain', 'cfg-domain-error');
  let hasError = false;
  if (!username) {
    setFieldError('cfg-username', 'cfg-username-error', 'Username is required.');
    hasError = true;
  }
  if (!domain) {
    setFieldError('cfg-domain', 'cfg-domain-error', 'Domain is required.');
    hasError = true;
  }
  if (hasError) {
    const firstBad = !username ? document.getElementById('cfg-username') : document.getElementById('cfg-domain');
    if (firstBad) firstBad.focus();
    return;
  }

  // Persist via backend: password -> OS keychain, rest -> ~/.kiskeya/account.json
  appConfig = { displayName, username, domain, proxy, protocol, stunServer, mediaEncryption, allowInsecureTLS };
  try {
    const keyErr = await App.SaveAccount(displayName, username, password, domain, proxy, protocol, stunServer, mediaEncryption, allowInsecureTLS);
    if (keyErr) {
      console.warn("Keychain save failed, password kept for this session only:", keyErr);
    }
  } catch (e) {
    console.error("Failed to save account:", e);
  }

  // Trigger Go registration
  App.RegisterSIP(displayName, username, password, domain, proxy, protocol, stunServer, mediaEncryption, allowInsecureTLS);
}

document.getElementById('btn-save-settings').addEventListener('click', saveAndRegister);
document.getElementById('btn-unregister').addEventListener('click', () => {
  App.UnregisterSIP();
});

// Clear validation styling once the user starts correcting the field.
document.getElementById('cfg-username').addEventListener('input', () => clearFieldError('cfg-username', 'cfg-username-error'));
document.getElementById('cfg-domain').addEventListener('input', () => clearFieldError('cfg-domain', 'cfg-domain-error'));

// --- TLS/SRTP settings clarity: insecure-TLS only meaningful for TLS transport ---
const cfgProtocol = document.getElementById('cfg-protocol');
const cfgInsecureTLS = document.getElementById('cfg-insecure-tls');
function syncInsecureTLSState() {
  const isTls = cfgProtocol.value === 'tls';
  cfgInsecureTLS.disabled = !isTls;
  // Visually de-emphasize the whole checkbox row when not applicable.
  const row = cfgInsecureTLS.closest('.form-checkbox');
  if (row) row.classList.toggle('disabled', !isTls);
}
cfgProtocol.addEventListener('change', syncInsecureTLSState);
syncInsecureTLSState();

// --- Dialer Display & Keypad Input ---
const dialDisplay = document.getElementById('dial-display');

function normalizeDialInput(value) {
  return value.replace(/[^0-9A-Za-z@._:+*#-]/g, '');
}

document.querySelectorAll('.keypad .key').forEach(key => {
  key.addEventListener('click', () => {
    const val = key.getAttribute('data-val');
    // During an active call the keypad sends in-band DTMF tones; otherwise it
    // edits the number being dialed.
    if (currentCallState === 'active') {
      App.SendDTMF(val);
      return;
    }
    dialDisplay.value += val;
    dialDisplay.focus();
  });
});

dialDisplay.addEventListener('input', () => {
  dialDisplay.value = normalizeDialInput(dialDisplay.value);
});

dialDisplay.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    document.getElementById('btn-dial').click();
  }
});

document.getElementById('btn-backspace').addEventListener('click', () => {
  dialDisplay.value = dialDisplay.value.slice(0, -1);
  dialDisplay.focus();
});

// --- Dialer Outgoing Calls ---
document.getElementById('btn-dial').addEventListener('click', () => {
  const number = dialDisplay.value.trim();
  if (number === "") return;
  App.MakeCall(number);
});

document.getElementById('btn-hangup').addEventListener('click', () => {
  App.HangupCall();
});

// Active Controls Action Mappings
const btnMute = document.getElementById('btn-mute');
let micMuted = false;
btnMute.addEventListener('click', () => {
  micMuted = !micMuted;
  App.MuteMicrophone(micMuted);
  if (micMuted) {
    btnMute.classList.add('active');
  } else {
    btnMute.classList.remove('active');
  }
});

// --- Contacts tab logic ---
const contactsListContainer = document.getElementById('contacts-list-container');

// Escape untrusted strings before inserting into innerHTML. Caller display names
// and numbers come from remote SIP headers (From), so they must never be treated
// as markup — otherwise a crafted caller could inject script (stored XSS).
function escapeHtml(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}
const contactSearch = document.getElementById('contact-search');

let allContacts = [];

function loadContacts() {
  App.GetContacts().then(list => {
    allContacts = list || [];
    renderContactsList(allContacts);
  });
}

function renderContactsList(contacts) {
  if (contacts.length === 0) {
    contactsListContainer.innerHTML = `
      <div class="empty-state">
        <span class="empty-state-icon">👤</span>
        <p>No contacts saved yet.</p>
      </div>
    `;
    return;
  }

  // NOTE: Wails serializes Go structs using their JSON tags, so fields arrive
  // lowercase (name/sipAddress/id) — not the Go PascalCase names.
  contactsListContainer.innerHTML = contacts.map(c => {
    const name = escapeHtml(c.name || '');
    const sip = escapeHtml(c.sipAddress || '');
    const id = escapeHtml(c.id || '');
    const initial = escapeHtml((c.name || '?').charAt(0).toUpperCase());
    return `
    <div class="list-item">
      <div class="list-item-main">
        <div class="list-item-avatar">${initial}</div>
        <div class="list-item-details">
          <div class="list-item-title">${name}</div>
          <div class="list-item-subtitle">${sip}</div>
        </div>
      </div>
      <div class="list-item-actions">
        <button type="button" class="icon-action-btn call-icon" title="Call" aria-label="Call ${name}" data-sip="${sip}">📞</button>
        <button type="button" class="icon-action-btn delete-icon" title="Delete" aria-label="Delete ${name}" data-confirm="0" data-id="${id}">🗑️</button>
      </div>
    </div>`;
  }).join('');
}

contactSearch.addEventListener('input', (e) => {
  const val = e.target.value.toLowerCase();
  const filtered = allContacts.filter(c => (c.name || '').toLowerCase().includes(val) || (c.sipAddress || '').toLowerCase().includes(val));
  renderContactsList(filtered);
});

const newContactNameEl = document.getElementById('new-contact-name');
const newContactUriEl = document.getElementById('new-contact-uri');
newContactNameEl.addEventListener('input', () => clearFieldError('new-contact-name', 'new-contact-name-error'));
newContactUriEl.addEventListener('input', () => clearFieldError('new-contact-uri', 'new-contact-uri-error'));

document.getElementById('btn-save-contact').addEventListener('click', () => {
  const nameEl = document.getElementById('new-contact-name');
  const uriEl = document.getElementById('new-contact-uri');
  const name = nameEl.value.trim();
  const uri = uriEl.value.trim();

  // Inline validation (no blocking alert)
  clearFieldError('new-contact-name', 'new-contact-name-error');
  clearFieldError('new-contact-uri', 'new-contact-uri-error');
  let hasError = false;
  if (!name) {
    setFieldError('new-contact-name', 'new-contact-name-error', 'Name is required.');
    hasError = true;
  }
  if (!uri) {
    setFieldError('new-contact-uri', 'new-contact-uri-error', 'SIP URI is required.');
    hasError = true;
  }
  if (hasError) {
    (!name ? nameEl : uriEl).focus();
    return;
  }

  App.SaveContact(name, uri).then(list => {
    allContacts = list || [];
    renderContactsList(allContacts);
    nameEl.value = '';
    uriEl.value = '';
  });
});

window.speedDial = function (uri) {
  dialDisplay.value = uri;
  // Jump to dialer
  document.querySelector('.nav-item[data-tab="dialer"]').click();
  App.MakeCall(uri);
};

// Non-blocking, accessible inline confirm: first click arms the button
// ("Confirm?"), a second click within 3s performs the delete.
let deleteConfirmTimer = null;
window.deleteContact = function (id, btn) {
  if (btn && btn.getAttribute('data-confirm') !== '1') {
    btn.setAttribute('data-confirm', '1');
    btn.classList.add('confirm-pending');
    btn.setAttribute('title', 'Click again to confirm delete');
    btn.setAttribute('aria-label', 'Confirm delete contact');
    btn.innerText = '✓?';
    clearTimeout(deleteConfirmTimer);
    deleteConfirmTimer = setTimeout(() => {
      if (!document.body.contains(btn)) return;
      btn.setAttribute('data-confirm', '0');
      btn.classList.remove('confirm-pending');
      btn.setAttribute('title', 'Delete');
      btn.setAttribute('aria-label', 'Delete contact');
      btn.innerText = '🗑️';
    }, 3000);
    return;
  }
  clearTimeout(deleteConfirmTimer);
  App.DeleteContact(id).then(list => {
    allContacts = list || [];
    renderContactsList(allContacts);
  });
};

// --- Call History Tab Logic ---
const historyListContainer = document.getElementById('history-list-container');

// Event delegation for call/delete actions. The list HTML carries the (escaped)
// SIP address / id in data-* attributes instead of inline JS, so untrusted caller
// data can never execute. dataset returns the browser-decoded original value.
contactsListContainer.addEventListener('click', (e) => {
  const callBtn = e.target.closest('.call-icon');
  if (callBtn && callBtn.dataset.sip != null) { window.speedDial(callBtn.dataset.sip); return; }
  const delBtn = e.target.closest('.delete-icon');
  if (delBtn && delBtn.dataset.id != null) { window.deleteContact(delBtn.dataset.id, delBtn); }
});
historyListContainer.addEventListener('click', (e) => {
  const callBtn = e.target.closest('.call-icon');
  if (callBtn && callBtn.dataset.sip != null) { window.speedDial(callBtn.dataset.sip); }
});

function loadCallHistory() {
  App.GetCallHistory().then(logs => {
    renderCallHistory(logs);
  });
}

function renderCallHistory(logs) {
  if (!logs || logs.length === 0) {
    historyListContainer.innerHTML = `
      <div class="empty-state">
        <span class="empty-state-icon">🕒</span>
        <p>No call logs available.</p>
      </div>
    `;
    return;
  }

  historyListContainer.innerHTML = logs.map(l => {
    const date = escapeHtml(new Date(l.timestamp).toLocaleString());
    const duration = escapeHtml(formatCallTime(l.durationSec));
    const directionClass = l.direction === 'incoming' ? 'incoming' : 'outgoing';
    const statusClass = l.status === 'answered' ? 'answered' : (l.status === 'missed' ? 'missed' : 'failed');
    const statusText = escapeHtml((l.status || '').charAt(0).toUpperCase() + (l.status || '').slice(1));
    // l.number originates from the remote SIP From header — must be escaped.
    const number = escapeHtml(l.number || '');

    return `
      <div class="list-item">
        <div class="list-item-main">
          <div class="list-item-avatar">${l.direction === 'incoming' ? '📥' : '📤'}</div>
          <div class="list-item-details">
            <div class="list-item-title">${number}</div>
            <div class="list-item-subtitle">
              <span class="direction-badge ${directionClass}">${escapeHtml(l.direction || '')}</span>
              <span class="call-status-label ${statusClass}">${statusText} (${duration})</span>
              <span class="history-date">${date}</span>
            </div>
          </div>
        </div>
        <div class="list-item-actions">
          <button type="button" class="icon-action-btn call-icon" title="Call" aria-label="Call ${number}" data-sip="${number}">📞</button>
        </div>
      </div>
    `;
  }).join('');
}

// Non-blocking inline confirm for clearing history: first click arms the
// button, a second click within 3s performs the clear.
const btnClearHistory = document.getElementById('btn-clear-history');
let clearHistoryTimer = null;
function resetClearHistoryBtn() {
  clearTimeout(clearHistoryTimer);
  btnClearHistory.classList.remove('confirm-pending');
  btnClearHistory.innerText = 'Clear Logs';
}
btnClearHistory.addEventListener('click', () => {
  if (!btnClearHistory.classList.contains('confirm-pending')) {
    btnClearHistory.classList.add('confirm-pending');
    btnClearHistory.innerText = 'Click again to confirm';
    clearTimeout(clearHistoryTimer);
    clearHistoryTimer = setTimeout(resetClearHistoryBtn, 3000);
    return;
  }
  resetClearHistoryBtn();
  App.ClearCallHistory().then(logs => {
    renderCallHistory(logs);
  });
});

// --- Audio Hardware Devices Settings ---
const selectMic = document.getElementById('select-mic');
const selectSpeaker = document.getElementById('select-speaker');

function loadAudioDevices() {
  App.GetAudioDevices().then(devices => {
    if (devices.error) {
      console.error(devices.error);
      return;
    }

    // Populate mic/speaker selects (device names are OS-provided; escape anyway).
    selectMic.innerHTML = devices.mics.map(d => `<option value="${escapeHtml(d.id)}">${escapeHtml(d.name)}</option>`).join('');
    selectSpeaker.innerHTML = devices.speakers.map(d => `<option value="${escapeHtml(d.id)}">${escapeHtml(d.name)}</option>`).join('');

    // Restore selected values from localStorage if saved
    const activeMic = localStorage.getItem('kiskeya_mic') || 'default';
    const activeSpeaker = localStorage.getItem('kiskeya_speaker') || 'default';

    if (devices.mics.some(d => d.id === activeMic)) selectMic.value = activeMic;
    if (devices.speakers.some(d => d.id === activeSpeaker)) selectSpeaker.value = activeSpeaker;

    App.SetAudioDevices(selectMic.value, selectSpeaker.value);
  });
}

function handleAudioDeviceChange() {
  localStorage.setItem('kiskeya_mic', selectMic.value);
  localStorage.setItem('kiskeya_speaker', selectSpeaker.value);
  App.SetAudioDevices(selectMic.value, selectSpeaker.value);
}

selectMic.addEventListener('change', handleAudioDeviceChange);
selectSpeaker.addEventListener('change', handleAudioDeviceChange);
document.getElementById('btn-refresh-audio').addEventListener('click', loadAudioDevices);

// --- Diagnostics Console Terminal Trace ---
const consoleLog = document.getElementById('console-log');
const diagnosticsFilter = document.getElementById('diagnostics-filter');

let allLogs = "";
let filterQuery = "";

function appendSIPLog(msg) {
  allLogs += msg;
  updateConsoleDisplay();
}

function updateConsoleDisplay() {
  if (!filterQuery) {
    consoleLog.innerText = allLogs;
  } else {
    // Filter lines
    const blocks = allLogs.split("----------------------------------------\n");
    const filteredBlocks = blocks.filter(b => b.toLowerCase().includes(filterQuery));
    consoleLog.innerText = filteredBlocks.join("----------------------------------------\n");
  }
  // Scroll to bottom
  consoleLog.scrollTop = consoleLog.scrollHeight;
}

diagnosticsFilter.addEventListener('input', (e) => {
  filterQuery = e.target.value.toLowerCase();
  updateConsoleDisplay();
});

document.getElementById('btn-clear-logs').addEventListener('click', () => {
  allLogs = "Console cleared...\n----------------------------------------\n";
  updateConsoleDisplay();
});

// --- Incoming Call Accept/Decline Actions ---
const incomingModal = document.getElementById('incoming-call-modal');
const incomingCallerId = document.getElementById('incoming-caller-id');
const btnIncomingAccept = document.getElementById('btn-incoming-accept');
const btnIncomingDecline = document.getElementById('btn-incoming-decline');

// Remember what had focus before the modal opened so we can restore it.
let preModalFocus = null;

function showIncomingModal() {
  preModalFocus = document.activeElement;
  incomingModal.style.display = 'flex';
  // Move focus to the primary (accept) action.
  setTimeout(() => btnIncomingAccept.focus(), 0);
}

function hideIncomingModal() {
  if (incomingModal.style.display === 'none') return;
  incomingModal.style.display = 'none';
  // Restore focus to wherever it was before the modal appeared.
  if (preModalFocus && document.body.contains(preModalFocus) && typeof preModalFocus.focus === 'function') {
    preModalFocus.focus();
  }
  preModalFocus = null;
}

function acceptIncoming() {
  App.AnswerCall();
  hideIncomingModal();
}

function declineIncoming() {
  App.RejectCall();
  hideIncomingModal();
}

btnIncomingAccept.addEventListener('click', acceptIncoming);
btnIncomingDecline.addEventListener('click', declineIncoming);

// Keyboard handling while the modal is visible: Enter = accept, Escape = decline.
// Also keep focus trapped between the two action buttons via Tab.
incomingModal.addEventListener('keydown', (event) => {
  if (incomingModal.style.display === 'none') return;
  if (event.key === 'Escape') {
    event.preventDefault();
    declineIncoming();
  } else if (event.key === 'Enter') {
    event.preventDefault();
    acceptIncoming();
  } else if (event.key === 'Tab') {
    // Simple two-button focus trap.
    event.preventDefault();
    const next = document.activeElement === btnIncomingAccept ? btnIncomingDecline : btnIncomingAccept;
    next.focus();
  }
});

// --- Helper Functions ---
function formatCallTime(sec) {
  const m = Math.floor(sec / 60).toString().padStart(2, '0');
  const s = (sec % 60).toString().padStart(2, '0');
  return `${m}:${s}`;
}

// --- Wails Event Listeners (Incoming Event Bridges) ---

// 1. SIP Logs
EventsOn("sip:log", (msg) => {
  appendSIPLog(msg);
});

// 2. SIP Call State Handler
const callStateLabel = document.getElementById('call-state-label');
const callTimerDisplay = document.getElementById('call-timer-display');
const btnDial = document.getElementById('btn-dial');
const btnHangup = document.getElementById('btn-hangup');
const valActiveCodec = document.getElementById('val-active-codec');

EventsOn("sip:call_state", (data) => {
  const state = data.state;
  currentCallState = state;

  callStateLabel.innerText = state.toUpperCase();

  if (state === 'idle') {
    // Stop timers & Reset UI
    clearInterval(callTimerInterval);
    callTimerInterval = null;
    callTimerDisplay.style.display = 'none';
    btnDial.style.display = 'flex';
    btnHangup.style.display = 'none';
    dialDisplay.readOnly = false;
    valActiveCodec.innerText = "None";
    const secElIdle = document.getElementById('val-media-security');
    if (secElIdle) {
      secElIdle.innerText = '—';
      secElIdle.classList.remove('secure', 'insecure');
      secElIdle.setAttribute('aria-label', 'Media security: none');
    }
    hideIncomingModal();

    // Clear the dialed number so it doesn't linger after the call ends.
    dialDisplay.value = '';

    // Disable Call control buttons. If the mic was muted, reset the backend
    // mute state so the next call starts un-muted.
    btnMute.disabled = true;
    if (micMuted) {
      App.MuteMicrophone(false);
    }
    micMuted = false;
    btnMute.classList.remove('active');

    // Reset Audio Bars
    const fillMicIdle = document.getElementById('fill-mic-level');
    const barMicIdle = document.getElementById('bar-mic-level');
    const fillSpkIdle = document.getElementById('fill-speaker-level');
    const barSpkIdle = document.getElementById('bar-speaker-level');
    fillMicIdle.style.width = '0%';
    document.getElementById('val-mic-level').innerText = '0%';
    if (barMicIdle) barMicIdle.setAttribute('aria-valuenow', '0');
    fillSpkIdle.style.width = '0%';
    document.getElementById('val-speaker-level').innerText = '0%';
    if (barSpkIdle) barSpkIdle.setAttribute('aria-valuenow', '0');
    // Reset rate-limit caches so the first level of the next call always writes.
    if (typeof lastMicAriaVal !== 'undefined') lastMicAriaVal = -1;
    if (typeof lastSpeakerAriaVal !== 'undefined') lastSpeakerAriaVal = -1;

    // Remove active-call body class and reset SVG logo bars
    document.body.classList.remove('call-active');
    document.querySelectorAll('.logo-icon .wave-bar').forEach((bar) => {
      bar.style.transform = '';
    });
  }
  
  else if (state === 'dialing' || state === 'ringing') {
    btnDial.style.display = 'none';
    btnHangup.style.display = 'flex';
    dialDisplay.readOnly = true;

    if (data.incoming) {
      // Ringing Incoming Call
      incomingCallerId.innerText = data.remoteParty;
      showIncomingModal();
    } else {
      // Dialing Outgoing Call
      dialDisplay.value = data.remoteParty;
    }
  }

  else if (state === 'active') {
    hideIncomingModal();
    btnDial.style.display = 'none';
    btnHangup.style.display = 'flex';
    dialDisplay.readOnly = true;
    valActiveCodec.innerText = data.codec;

    // Reflect negotiated media security (SRTP vs plaintext RTP).
    // The lock/unlock glyph + word ensure secure/insecure never rely on hue alone.
    const secEl = document.getElementById('val-media-security');
    if (secEl) {
      if (data.secure) {
        secEl.innerText = '🔒 SRTP';
        secEl.setAttribute('aria-label', 'Media security: encrypted with SRTP');
        secEl.classList.add('secure');
        secEl.classList.remove('insecure');
      } else {
        secEl.innerText = '🔓 Unencrypted';
        secEl.setAttribute('aria-label', 'Media security: unencrypted');
        secEl.classList.add('insecure');
        secEl.classList.remove('secure');
      }
    }

    // Enable Mute Action
    btnMute.disabled = false;

    // Add active-call body class
    document.body.classList.add('call-active');

    // Start Call Duration Timer
    callDurationSec = 0;
    callTimerDisplay.innerText = "00:00";
    callTimerDisplay.style.display = 'block';

    if (!callTimerInterval) {
      callTimerInterval = setInterval(() => {
        callDurationSec++;
        callTimerDisplay.innerText = formatCallTime(callDurationSec);
      }, 1000);
    }
  }
});

// 3. SIP Registration State Handler
const sidebarProfileName = document.getElementById('sidebar-profile-name');
const sidebarStatusDot = document.getElementById('sidebar-status-dot');
const sidebarStatusText = document.getElementById('sidebar-status-text');
const topbarStatusDot = document.getElementById('topbar-status-dot');
const topbarStatusText = document.getElementById('topbar-status-text');
const settingsStatusCard = document.getElementById('settings-status-card');
const settingsStatusValue = document.getElementById('settings-status-value');
const settingsStatusError = document.getElementById('settings-status-error');

EventsOn("sip:reg_state", (data) => {
  const state = data.state;
  registrationState = state;

  // Reset classes
  sidebarStatusDot.className = "status-dot " + state;
  topbarStatusDot.className = "status-dot " + state;
  settingsStatusCard.className = "status-badge-card " + state;

  if (state === 'idle') {
    sidebarProfileName.innerText = "Not Registered";
    sidebarStatusText.innerText = "Disconnected";
    topbarStatusText.innerText = "Offline";
    settingsStatusValue.innerText = "Unregistered";
    settingsStatusError.style.display = 'none';
  } 
  
  else if (state === 'registering') {
    sidebarStatusText.innerText = "Connecting...";
    topbarStatusText.innerText = "Connecting";
    settingsStatusValue.innerText = "Registering...";
    settingsStatusError.style.display = 'none';
  } 
  
  else if (state === 'registered') {
    const cfg = appConfig || {};
    const profileText = cfg.displayName ? `${cfg.displayName} (${cfg.username})` : cfg.username;
    sidebarProfileName.innerText = profileText;
    sidebarStatusText.innerText = "Online";
    topbarStatusText.innerText = profileText ? `Online as ${profileText}` : "Online";
    settingsStatusValue.innerText = "Registered";
    settingsStatusError.style.display = 'none';
  } 
  
  else if (state === 'failed') {
    sidebarStatusText.innerText = "Registration Failed";
    topbarStatusText.innerText = "Registration failed";
    settingsStatusValue.innerText = "Failed";
    settingsStatusError.innerText = data.error;
    settingsStatusError.style.display = 'block';
  }
});

// 4. Audio Levels Event Handler
const fillMicLevel = document.getElementById('fill-mic-level');
const valMicLevel = document.getElementById('val-mic-level');
const barMicLevel = document.getElementById('bar-mic-level');
const fillSpeakerLevel = document.getElementById('fill-speaker-level');
const valSpeakerLevel = document.getElementById('val-speaker-level');
const barSpeakerLevel = document.getElementById('bar-speaker-level');

// Rate-limit aria-valuenow writes so screen readers aren't flooded by the
// high-frequency level events. Only update when the rounded value changes.
let lastMicAriaVal = -1;
let lastSpeakerAriaVal = -1;

EventsOn("audio:mic_level", (level) => {
  if (currentCallState === 'active') {
    const rounded = Math.round(level);
    fillMicLevel.style.width = rounded + '%';
    valMicLevel.innerText = rounded + '%';
    if (barMicLevel && rounded !== lastMicAriaVal) {
      barMicLevel.setAttribute('aria-valuenow', String(rounded));
      lastMicAriaVal = rounded;
    }

    // Scale the dynamic SVG logo wave bars in the sidebar matching voice input!
    const scaleFactor = 1.0 + (level / 100.0) * 1.5;
    document.querySelectorAll('.logo-icon .wave-bar').forEach((bar, idx) => {
      const delayScale = 1.0 + Math.sin(idx + (level / 10.0)) * (level / 200.0);
      const val = Math.max(1.0, scaleFactor * delayScale);
      bar.style.transform = `scaleY(${val})`;
    });
  }
});

EventsOn("audio:speaker_level", (level) => {
  if (currentCallState === 'active') {
    const rounded = Math.round(level);
    fillSpeakerLevel.style.width = rounded + '%';
    valSpeakerLevel.innerText = rounded + '%';
    if (barSpeakerLevel && rounded !== lastSpeakerAriaVal) {
      barSpeakerLevel.setAttribute('aria-valuenow', String(rounded));
      lastSpeakerAriaVal = rounded;
    }
  }
});

// 5. History update listener
EventsOn("history:updated", (logs) => {
  renderCallHistory(logs);
});

// --- Theme Application System ---
function applyTheme(themeName) {
  document.body.classList.remove('theme-sunset', 'theme-emerald', 'theme-ocean');
  if (themeName !== 'default') {
    document.body.classList.add(`theme-${themeName}`);
  }
  localStorage.setItem('kiskeya_theme', themeName);
}

// --- Initial Setup on Mount ---
document.addEventListener('DOMContentLoaded', () => {
  loadAccountSettings();
  loadContacts();
  loadCallHistory();
  loadAudioDevices();

  // Load and apply theme setting
  const savedTheme = localStorage.getItem('kiskeya_theme') || 'default';
  const themeSelect = document.getElementById('select-theme');
  if (themeSelect) {
    themeSelect.value = savedTheme;
    themeSelect.addEventListener('change', (e) => {
      applyTheme(e.target.value);
    });
  }
  applyTheme(savedTheme);
});
