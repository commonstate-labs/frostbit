                                                                                   
                                                                                           
                                                                                                     
export function installTooltip() {
  const tip = document.createElement('div')
  tip.className = 'app-tooltip'
  tip.id = 'app-tooltip'
  tip.setAttribute('role', 'tooltip')
  document.body.appendChild(tip)

  let current: HTMLElement | null = null

  const migrate = (el: Element) => {
    const raw = el.getAttribute('title')
    if (raw === null) return
    if (raw) el.setAttribute('data-tip', raw)
    el.removeAttribute('title')
  }

  const sweep = (root: ParentNode) => {
    if (root instanceof Element && root.hasAttribute('title')) migrate(root)
    root.querySelectorAll?.('[title]').forEach(migrate)
  }

  sweep(document.body)

  const mo = new MutationObserver((records) => {
    for (const r of records) {
      if (r.type === 'attributes' && r.target instanceof Element) migrate(r.target)
      else if (r.type === 'childList') r.addedNodes.forEach((n) => n instanceof Element && sweep(n))
    }
  })
  mo.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['title'] })

  const resolve = (t: EventTarget | null): HTMLElement | null =>
    t instanceof Element ? (t.closest('[data-tip]') as HTMLElement | null) : null

  const releaseDescription = (el: HTMLElement | null) => {
    if (el?.dataset.appTooltipOwnsDescription !== 'true') return
    el.removeAttribute('aria-describedby')
    delete el.dataset.appTooltipOwnsDescription
  }

  const hide = () => {
    releaseDescription(current)
    current = null
    tip.classList.remove('visible')
  }

  const place = (cx: number, cy: number) => {
    const pad = 14, vw = window.innerWidth, vh = window.innerHeight
    const w = tip.offsetWidth, h = tip.offsetHeight
    let x = cx + pad, y = cy + pad
    if (x + w + pad > vw) x = cx - pad - w
    if (y + h + pad > vh) y = cy - pad - h
    tip.style.left = Math.max(4, x) + 'px'
    tip.style.top = Math.max(4, y) + 'px'
  }

  const show = (el: HTMLElement, cx: number, cy: number) => {
    const text = el.getAttribute('data-tip') || ''
    if (!text) return
    if (current !== el) releaseDescription(current)
    current = el
    if (!el.hasAttribute('aria-describedby')) {
      el.setAttribute('aria-describedby', tip.id)
      el.dataset.appTooltipOwnsDescription = 'true'
    }
    tip.textContent = text
    tip.classList.add('visible')
    place(cx, cy)
  }

  document.addEventListener('mouseover', (e) => {
    const el = resolve(e.target)
    if (!el || el === current) return
    show(el, (e as MouseEvent).clientX, (e as MouseEvent).clientY)
  }, true)

  document.addEventListener('mousemove', (e) => {
    if (!current) return
    place((e as MouseEvent).clientX, (e as MouseEvent).clientY)
  })

  document.addEventListener('mouseout', (e) => {
    if (!current) return
    const r = (e as MouseEvent).relatedTarget as Node | null
    if (r && current.contains(r)) return
    if (document.activeElement === current) return
    hide()
  }, true)

  document.addEventListener('focusin', (e) => {
    const el = resolve(e.target)
    if (!el) return
    const rect = el.getBoundingClientRect()
    show(el, rect.right, rect.bottom)
  }, true)

  document.addEventListener('focusout', (e) => {
    if (!current) return
    const next = (e as FocusEvent).relatedTarget as Node | null
    if (next && current.contains(next)) return
    hide()
  }, true)

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && current) hide()
  })

  window.addEventListener('scroll', hide, true)
  window.addEventListener('blur', hide)
}
