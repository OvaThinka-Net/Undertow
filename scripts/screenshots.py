#!/usr/bin/env python3
"""Take screenshots of the Undertow admin panel for README documentation."""
import sys, os
sys.path.insert(0, os.path.expanduser("~/Library/Python/3.9/lib/python/site-packages"))

from playwright.sync_api import sync_playwright

BASE = "http://192.168.0.66:8090"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "assets", "screenshots")
os.makedirs(OUT, exist_ok=True)

REDACT_JS = """
() => {
    const FOLDER_ID_RE = /1[A-Za-z0-9_-]{25,}/g;
    const REPLACE = '•••••••••••••••••••';

    // Redact all text nodes
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    while (walk.nextNode()) {
        const node = walk.currentNode;
        node.textContent = node.textContent.replace(FOLDER_ID_RE, REPLACE);
    }

    // Redact input and textarea values
    document.querySelectorAll('input, textarea').forEach(el => {
        if (el.value) el.value = el.value.replace(FOLDER_ID_RE, REPLACE);
    });

    // Redact contenteditable and innerText of code blocks
    document.querySelectorAll('textarea, pre, code, [contenteditable]').forEach(el => {
        if (el.innerText) {
            el.innerText = el.innerText.replace(FOLDER_ID_RE, REPLACE);
        }
    });

    // Also handle username field - replace with generic
    document.querySelectorAll('input').forEach(el => {
        if (el.value === 'REDACTED_USER') el.value = 'admin';
    });
}
"""

def screenshot(page, name, full_page=False):
    path = os.path.join(OUT, name)
    page.screenshot(path=path, full_page=full_page)
    print(f"  ✓ {name}")

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    ctx = browser.new_context(viewport={"width": 1280, "height": 800})
    page = ctx.new_page()

    # 1. Login page
    print("1. Login page...")
    page.goto(BASE + "/login")
    page.wait_for_load_state("networkidle")
    screenshot(page, "01-login.png")

    # 2. Login
    print("2. Logging in...")
    page.fill('#username', 'REDACTED_USER')
    page.fill('#password', 'REDACTED_PASS')
    page.click('button[type="submit"]')
    page.wait_for_timeout(2000)
    print(f"   Now at: {page.url}")

    # 3. Dashboard
    print("3. Dashboard...")
    page.goto(BASE + "/")
    page.wait_for_timeout(2000)
    page.evaluate(REDACT_JS)
    page.wait_for_timeout(300)
    screenshot(page, "02-dashboard.png")
    screenshot(page, "02-dashboard-full.png", full_page=True)

    # 4. Setup Wizard
    print("4. Setup Wizard...")
    try:
        page.click("button:has-text('Setup Wizard')")
        page.wait_for_timeout(1500)
        page.evaluate(REDACT_JS)
        page.wait_for_timeout(300)
        screenshot(page, "03-setup-wizard.png")
        screenshot(page, "03-setup-wizard-full.png", full_page=True)
    except Exception as e:
        print(f"   Could not find wizard button: {e}")

    browser.close()
    print(f"\nDone! Screenshots saved to {OUT}")
