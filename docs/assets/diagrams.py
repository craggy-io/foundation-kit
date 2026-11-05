"""Generates the documentation diagrams in this folder and checks that no
text overflows its box. Run: python3 docs/assets/diagrams.py

Design rules: one idea per diagram, a strict top-to-bottom (or left-to-right)
hierarchy, at most three boxes per tier, a title and one short line per box.
The palette and type follow the other Craggy services repositories.
"""
import os
import subprocess
import sys
from xml.sax.saxutils import escape as esc

HERE = os.path.dirname(os.path.abspath(__file__))
AMB, TXT, DIM, BG, CARD, EDGE, RULE = '#FFB547', '#E4ECE7', '#93A39A', '#111A20', '#16222A', '#4A3F2C', '#2A2F2C'
FONT = "'JetBrains Mono', Consolas, 'Liberation Mono', monospace"
ADV = 0.62  # monospace advance per em, with margin


class SVG:
    def __init__(self, w, h, title, desc):
        self.w, self.h = w, h
        self.out = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-labelledby="title desc">',
            f'<title id="title">{esc(title)}</title>',
            f'<desc id="desc">{esc(desc)}</desc>',
            f'<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M1 1 L9 5 L1 9" fill="none" stroke="{AMB}" stroke-width="1.5"/></marker></defs>',
            f'<rect width="{w}" height="{h}" rx="20" fill="{BG}"/>',
            f'<g font-family="{FONT}">',
        ]

    def text(self, x, y, t, size=18, fill=TXT, bold=False, anchor='start'):
        self.out.append(f'<text x="{x}" y="{y}" font-size="{size}" fill="{fill}" font-weight="{700 if bold else 400}" text-anchor="{anchor}">{esc(t)}</text>')

    def header(self, title, sub, tag):
        self.text(40, 53, '> ' + title, 28, AMB, True)
        self.text(40, 87, sub, 20, DIM)
        self.text(self.w - 40, 53, tag, 18, TXT, anchor='end')
        self.out.append(f'<path d="M40 111 H{self.w - 40}" fill="none" stroke="{RULE}" stroke-width="2"/>')

    def box(self, x, y, w, h, title, line):
        self.out.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="12" fill="{CARD}" stroke="{EDGE}" stroke-width="1.5"/>')
        self.text(x + w // 2, y + 38, title, 22, TXT, True, 'middle')
        self.text(x + w // 2, y + 68, line, 16, DIM, anchor='middle')

    def arrow(self, d):
        self.out.append(f'<path d="{d}" fill="none" stroke="{AMB}" stroke-width="2" marker-end="url(#arrow)"/>')

    def save(self, name):
        self.out.append('</g></svg>')
        with open(os.path.join(HERE, name), 'w') as f:
            f.write('\n'.join(self.out) + '\n')


def width(title, line):
    return int(max(len(title) * 22, len(line) * 16) * ADV) + 48


# --- architecture: layers, imports only point down ---------------------------
tiers = [
    ('CONSUMERS', [('Your code', 'applications and libraries')]),
    ('EXTENSIONS', [('bundle/compile', 'text, JSON, YAML'), ('kind/jsonschema', 'JSON Schema for kinds')]),
    ('ENGINES', [('bundle', 'catalogs, overrides, Live'), ('settings', 'live, editable values'), ('small engines', 'fsx, canonjson, retryafter')]),
    ('CONTRACTS', [('kind', 'versioned kinds, built-ins')]),
    ('STDLIB', [('Go standard library', 'no other dependencies')]),
]
W, BOX_H, TIER_GAP, LEFT = 1280, 92, 44, 220
right_edge = W - 40
boxw = max(width(t, l) for _, row in tiers for t, l in row)
H = 140 + len(tiers) * BOX_H + (len(tiers) - 1) * TIER_GAP + 100
a = SVG(W, H, 'Foundation Kit layers',
        'Five tiers, top to bottom: your applications and libraries; extensions (bundle/compile, kind/jsonschema); engines (bundle, settings, and the small engines fsx, canonjson and retryafter); contracts (kind); the Go standard library. Imports only point down, and a layer may import any layer below it. Test support (bundletest, archtest) sits alongside the layers and is imported only by tests.')
a.header('FOUNDATION KIT', 'Shared foundations for Craggy applications and libraries', 'PACKAGE LAYERS')
span = right_edge - LEFT
y = 140
for i, (label, row) in enumerate(tiers):
    a.text(40, y + BOX_H // 2 + 7, label, 17, AMB, True)
    gap = 40
    total = len(row) * boxw + (len(row) - 1) * gap
    x = LEFT + (span - total) // 2
    for t, l in row:
        a.box(x, y, boxw, BOX_H, t, l)
        x += boxw + gap
    if i < len(tiers) - 1:
        cx = LEFT + span // 2
        a.arrow(f'M{cx} {y + BOX_H + 4} V{y + BOX_H + TIER_GAP - 4}')
        a.text(cx + 14, y + BOX_H + TIER_GAP // 2 + 6, 'imports', 16, AMB)
    y += BOX_H + TIER_GAP
a.text(40, H - 62, 'Imports only point down; a layer may import any layer below it.', 16, DIM)
a.text(40, H - 34, 'Test support (bundletest, archtest) sits alongside; only tests import it.', 16, DIM)
a.save('foundation-kit-architecture.svg')

# --- bundle.Load pipeline: seven stages, left to right -----------------------
steps = [('Locate', 'open sources'), ('Read', 'snapshot'), ('Validate', 'paths, IDs'),
         ('Compile', 'concurrently'), ('Check', 'kind schemas'), ('Link', 'resolve refs'),
         ('Accept', 'digests')]
W2 = 1440
bw = max(width(t, l) for t, l in steps)
gap2 = (W2 - 80 - len(steps) * bw) // (len(steps) - 1)
assert gap2 >= 24, gap2
p = SVG(W2, 360, 'How bundle.Load builds a catalog',
        'Seven stages from left to right: locate each source, read a snapshot and parse bundle.yaml, validate manifests, paths, IDs, kinds and assets, compile contributions concurrently, check compiled values and built-ins against kind schemas, link definitions, overrides and references, then accept: compute digests and publish the immutable catalog. Each stage collects every problem, and only runs on input that passed the stage before it.')
p.header('BUNDLE.LOAD', 'Sources and built-ins in, one immutable catalog out', 'PIPELINE')
x, y = 40, 170
for i, (t, l) in enumerate(steps):
    p.text(x, y - 12, f'0{i + 1}', 16, AMB, True)
    p.box(x, y, bw, BOX_H, t, l)
    if i < len(steps) - 1:
        p.arrow(f'M{x + bw + 4} {y + BOX_H // 2} H{x + bw + gap2 - 4}')
    x += bw + gap2
p.text(40, y + BOX_H + 52, 'Each stage reports every problem it finds, and only runs on input that passed the one before.', 16, DIM)
p.text(40, y + BOX_H + 80, 'Nothing is started: services read what they need from the catalog.', 16, DIM)
p.save('bundle-pipeline.svg')

sys.exit(subprocess.call([sys.executable, os.path.join(HERE, 'check_svg.py'),
                          os.path.join(HERE, 'foundation-kit-architecture.svg'),
                          os.path.join(HERE, 'bundle-pipeline.svg')]))
