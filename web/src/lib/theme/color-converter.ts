// @ts-nocheck
/**
 * Color conversion helpers for Flow theme generation (LS-0692).
 * LCH adjust/mix + APCA text contrast — algorithmic parity with Linear ColorConverter.
 */


function n_1(fn) {
  let ran = false;
  const run = () => { if (!ran) { ran = true; f } };
  return run;
}

!(() => {
  try {
    const e =
      typeof window !== "undefined"
        ? window
        : typeof global !== "undefined"
          ? global
          : typeof globalThis !== "undefined"
            ? globalThis
            : typeof self !== "undefined"
              ? self
              : {};
    const n = new e.Error().stack;
    if (n) {
      e._sentryDebugIds = e._sentryDebugIds || {};
      e._sentryDebugIds[n] = "2d763f61-e913-556d-929c-59eb7510aac0";
    }
  } catch (e) {}
})();
function t(e, t, n) {
  return Math.max(t, Math.min(n, e));
}

function r(e) {
  return parseFloat(e.toFixed(3));
}
function i(e) {
  if (e >= o) {
    return e;
  }
  return e + (o - e) ** s;
}
let a;
var o;
var s;
function initColorConverter() {
  
  ((e) => {
    let n = (e.HEX_REGEX_LOOSE =
      /#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})?/i);
    let o = (e.HEX_REGEX = RegExp(`^${n.source}$`, `i`));
    let s = (e.HEX_REGEX_SMALL_LOOSE = /#?([a-f\d])([a-f\d])([a-f\d])/i);
    let c = (e.HEX_REGEX_SMALL = RegExp(`^${s.source}$`, `i`));
    let l = (e.HEX_REGEX_STRICT = /^#([a-f\d]{6}|[a-f\d]{3})$/i);
    let u = (e.LCH_REGEX_LOOSE =
      /lch\((\d{1,3}(?:\.\d+)?)\% (\d{1,3}(?:\.\d+)?) (\d{1,3}(?:\.\d+)?)(?: \/ ([1|0](?:\.\d+)?)?)?\)/i);
    let d = (e.LCH_REGEX = RegExp(`^${u.source}$`, `i`));
    let f = (e.P3_REGEX_LOOSE =
      /color\(display-p3 (\d{1,3}(?:\.\d+)?)\ (\d{1,3}(?:\.\d+)?) (\d{1,3}(?:\.\d+)?)(?: \/ ([1|0](?:\.\d+)?)?)?\)/i);
    let p = (e.P3_REGEX = RegExp(`^${f.source}$`, `i`));
    let m = (e.ANY_COLOR_REGEX_LOOSE = RegExp(
      `(?:${n.source})|(?:${u.source})|(?:${f.source})`,
      `i`,
    ));
    let h = (e.ANY_COLOR_REGEX = RegExp(`^${m.source}$`, `i`));
    let g;
    function _() {
      return (
        g ||
        ((g =
          typeof window > `u` || window.CSS === undefined
            ? `RGB`
            : window.CSS.supports(`color`, `lch(0% 0 0)`)
              ? `LCH`
              : window.CSS.supports(`color`, `color(display-p3 0 0 0)`)
                ? `P3`
                : `RGB`),
        g)
      );
    }
    e.getColorFormat = _;
    let v = [0.3457 / 0.3585, 1, 0.2958 / 0.3585];
    function y(e, [t, n, i, a]) {
      if (e === `LCH`) {
        return `lch(${r(t)}% ${r(n)} ${r(i)}${a === undefined ? `` : ` / ` + r(a)})`;
      }
      if (e === `P3`) {
        return N([t, n, i, a]);
      }
      return k([t, n, i, a]);
    }
    e.toCss = y;
    function b(e) {
      let t = o.exec(e);
      t ??
        ((t = c.exec(e)),
        t && ((t[1] += t[1]), (t[2] += t[2]), (t[3] += t[3])));
      if (t) {
        let e = a.rgbToLch([
          parseInt(t[1], 16),
          parseInt(t[2], 16),
          parseInt(t[3], 16),
        ]);
        if (t[4] && !/ff/i.test(t[4])) {
          e[3] = parseInt(t[4], 16) / 255;
        }
        return e;
      } else {
        let t = d.exec(e);
        if (t) {
          let e = [parseFloat(t[1]), parseFloat(t[2]), parseFloat(t[3])];
          if (t[4] && t[4] !== `1`) {
            e[3] = parseFloat(t[4]);
          }
          return e;
        } else {
          let t = p.exec(e);
          if (t) {
            let e = R([parseFloat(t[1]), parseFloat(t[2]), parseFloat(t[3])]);
            if (t[4] && t[4] !== `1`) {
              e[3] = parseFloat(t[4]);
            }
            return e;
          }
        }
      }
      return [0, 0, 0];
    }
    e.fromCss = b;
    function x(e, a_1, n = _()) {
      return a.toCss(
        n,
        a.adjustTo(a.fromCss(e), {
          a: a_1,
        }),
      );
    }
    e.cssWithAlpha = x;
    function S([t, n, r]) {
      return [t - n * 0.075 > 65 ? 0 : 100, Math.min(n / 2, n), r];
    }
    e.getTextColor = S;
    function C(e, t) {
      let [n, r, i] = Q(e);
      let [a, o, s] = Q(t);
      let c = Math.sqrt(r * r + i * i);
      let l = Math.sqrt(o * o + s * s);
      let u = n < 16 ? 0.511 : (0.040975 * n) / (1 + 0.01765 * n);
      let d = (0.0638 * c) / (1 + 0.0131 * c) + 0.638;
      let f = c < 0.000001 ? 0 : (Math.atan2(i, r) * 180) / Math.PI;
      while (f < 0) {
        f += 360;
      }
      while (f >= 360) {
        f -= 360;
      }
      let p =
        f >= 164 && f <= 345
          ? 0.56 + Math.abs(0.2 * Math.cos((Math.PI * (f + 168)) / 180))
          : 0.36 + Math.abs(0.4 * Math.cos((Math.PI * (f + 35)) / 180));
      let m = c * c * c * c;
      let h = Math.sqrt(m / (m + 1900));
      let g = d * (h * p + 1 - h);
      let _ = n - a;
      let v = c - l;
      let y = r - o;
      let b = i - s;
      let x = y * y + b * b - v * v;
      let S = _ / (1 * u);
      let C = v / (3 * d);
      let w = g;
      return Math.sqrt(S * S + C * C + x / (w * w));
    }
    e.deltaE = C;
    function w(e, t) {
      let n = 0.027;
      let r = e[0] / 100;
      let a = t[0] / 100;
      let o;
      let s;
      let c;
      let l = i(r);
      let u = i(a);
      let d = u > l;
      if (Math.abs(u - l) < 0.0005) {
        s = 0;
      } else if (d) {
        o = u ** 0.56 - l ** 0.57;
        s = o * 1.14;
      } else {
        o = u ** 0.65 - l ** 0.62;
        s = o * 1.14;
      }
      c = Math.abs(s) < 0.1 ? 0 : s > 0 ? s - n : s + n;
      return Math.abs(c * 100);
    }
    e.apcaContrast = w;
    function T(e, t, n = 38) {
      return w(e, t) > n;
    }
    e.sufficientContrastForText = T;
    function E(e) {
      return e[0] > 50;
    }
    e.isBright = E;
    function D([r, i, a, o = 1], n) {
      return [
        t(r + (n.l ?? 0), 0, 100),
        t(i + (n.c ?? 0), 0, 132),
        t(a + (n.h ?? 0), 0, 360),
        t(o + (n.a ?? 0), 0, 1),
      ];
    }
    e.adjust = D;
    function O([r, i, a, o = 1], n) {
      return [
        t(n.l ?? r, 0, 100),
        t(n.c ?? i, 0, 132),
        t(n.h ?? a, 0, 360),
        t(n.a ?? o, 0, 1),
      ];
    }
    e.adjustTo = O;
    function ee(e, n, r) {
      let i = e[3] ?? 1;
      let a = n[3] ?? 1;
      let [o, s, c] = X(Q(e));
      let [l, u, d] = X(Q(n));
      let [f, p, m] = Z(
        Y([o * (1 - r) + r * l, s * (1 - r) + r * u, c * (1 - r) + r * d]),
      );
      return [t(f, 0, 100), t(p, 0, 132), t(m, 0, 360), i * (1 - r) + a * r];
    }
    e.mix = ee;
    function te(e, t, n, r = _()) {
      return a.toCss(r, a.mix(a.fromCss(e), a.fromCss(t), n));
    }
    e.mixCss = te;
    function k(e) {
      let t = L(e)
        .map((e) => e.toString(16).split(`.`)[0])
        .map((e) => {
          if (e.length === 1) {
            return `0` + e;
          }
          return e;
        });
      let n =
        e[3] !== undefined && e[3] !== 1
          ? A((e[3] * 255).toString(16).split(`.`)[0])
          : ``;
      return `#${t[0]}${t[1]}${t[2]}${n}`;
    }
    e.lchToRgbString = k;
    function A(e) {
      if (e.length === 1) {
        return `0` + e;
      }
      return e;
    }
    function j(e, t) {
      if (!t?.format) {
        return h.test(e);
      }
      switch (t.format) {
        case `hex`:
          switch (t.level) {
            case `loose`:
              return n.test(e);
            case `small`:
              return c.test(e);
            default:
              return l.test(e);
          }
        case `p3`:
          return p.test(e);
        case `lch`:
          return d.test(e);
        default:
          return h.test(e);
      }
    }
    e.isValidColor = j;
    function M(e) {
      if (o.test(e)) {
        return e;
      }
      return k(b(e));
    }
    e.cssToRgb = M;
    function N(e) {
      let t = P(e);
      return `color(display-p3 ${t[0]} ${t[1]} ${t[2]}${e[3] === undefined ? `` : ` / ${e[3].toString(10)}`})`;
    }
    function P(e) {
      return K(q(X(Q(e))))
        .map(V)
        .map((e) => t(e, 0, 1));
    }
    function F(e) {
      return Z(Y(J(U($(e)))));
    }
    e.rgbToLch = F;
    function I(e, t, n, r) {
      let i = t;
      let a = [];
      for (let t = 0; t < e; t++) {
        i += 0.618033988749895;
        i %= 1;
        a[t] = k([n * 100, r * 100, i * 360]);
      }
      return a;
    }
    e.palette = I;
    function L(e) {
      return B(Q(e));
    }
    e.lchToRgb = L;
    function R(e) {
      return Z(Y(J(W($(e)))));
    }
    function z([[e, t, n], [r, i, a], [o, s, c]], [l, u, d]) {
      return [
        e * l + t * u + n * d,
        r * l + i * u + a * d,
        o * l + s * u + c * d,
      ];
    }
    e.multiplyMatrix = z;
    let B = (e) => {
      if (e[0] === 100 && e[1] === 0 && e[2] === 0) {
        return [255, 255, 255];
      }
      return H(G(q(X(e)))).map((e) => t(e, 0, 255));
    };
    function V(e) {
      let t = e < 0 ? -1 : 1;
      let n = Math.abs(e);
      if (n > 0.0031308) {
        return t * (1.055 * n ** (1 / 2.4) - 0.055);
      }
      return 12.92 * e;
    }
    function H(e) {
      return e.map((e) => 255 * V(e));
    }
    e.srgbGamma = H;
    function U(e) {
      return z(
        [
          [0.41239079926595934, 0.357584339383878, 0.1804807884018343],
          [0.21263900587151027, 0.715168678767756, 0.07219231536073371],
          [0.01933081871559182, 0.11919477979462598, 0.9505321522496607],
        ],
        e,
      );
    }
    function W(e) {
      return z(
        [
          [0.4865709486482162, 0.26566769316909306, 0.1982172852343625],
          [0.2289745640697488, 0.6917385218365064, 0.079286914093745],
          [0, 0.04511338185890264, 1.043944368900976],
        ],
        e,
      );
    }
    function G(e) {
      return z(
        [
          [3.2409699419045226, -1.537383177570094, -0.4986107602930034],
          [-0.9692436362808796, 1.8759675015077202, 0.04155505740717559],
          [0.05563007969699366, -0.20397695888897652, 1.0569715142428786],
        ],
        e,
      );
    }
    function K(e) {
      return z(
        [
          [2.493496911941425, -0.9313836179191239, -0.40271078445071684],
          [-0.8294889695615747, 1.7626640603183463, 0.023624685841943577],
          [0.03584583024378447, -0.07617238926804182, 0.9568845240076872],
        ],
        e,
      );
    }
    function q(e) {
      return z(
        [
          [0.9554734527042182, -0.023098536874261423, 0.0632593086610217],
          [-0.028369706963208136, 1.0099954580058226, 0.021041398966943008],
          [0.012314001688319899, -0.020507696433477912, 1.3303659366080753],
        ],
        e,
      );
    }
    function J(e) {
      return z(
        [
          [1.0479298208405488, 0.022946793341019088, -0.05019222954313557],
          [0.029627815688159344, 0.990434484573249, -0.01707382502938514],
          [-0.009243058152591178, 0.015055144896577895, 0.7518742899580008],
        ],
        e,
      );
    }
    function Y(e) {
      let t = e
        .map((e, t) => e / v[t])
        .map((e) => {
          if (e > 0.008856451679035631) {
            return Math.cbrt(e);
          }
          return (903.2962962962963 * e + 16) / 116;
        });
      return [116 * t[1] - 16, 500 * (t[0] - t[1]), 200 * (t[1] - t[2])];
    }
    function X(e) {
      let t = 24389 / 27;
      let n = 216 / 24389;
      let r = [];
      r[1] = (e[0] + 16) / 116;
      r[0] = e[1] / 500 + r[1];
      r[2] = r[1] - e[2] / 200;
      return [
        r[0] ** 3 > n ? r[0] ** 3 : (116 * r[0] - 16) / t,
        e[0] > t * n ? ((e[0] + 16) / 116) ** 3 : e[0] / t,
        r[2] ** 3 > n ? r[2] ** 3 : (116 * r[2] - 16) / t,
      ].map((e, t) => e * v[t]);
    }
    function Z(e) {
      let t = (Math.atan2(e[2], e[1]) * 180) / Math.PI;
      return [e[0], Math.sqrt(e[1] ** 2 + e[2] ** 2), t >= 0 ? t : t + 360, 1];
    }
    function Q(e) {
      return [
        e[0],
        e[1] * Math.cos((e[2] * Math.PI) / 180),
        e[1] * Math.sin((e[2] * Math.PI) / 180),
      ];
    }
    e.LCH_to_Lab = Q;
    function ne(e) {
      let t = e < 0 ? -1 : 1;
      let n = Math.abs(e);
      if (n < 0.04045) {
        return e / 12.92;
      }
      return t * ((n + 0.055) / 1.055) ** 2.4;
    }
    function $(e) {
      return e.map((e) => ne(e / 255));
    }
    e.srgbLinear = $;
  })((a ||= {}));
  o = 0.022;
  s = 1.414;
}
initColorConverter();

export const colorConverter = a;

/** Clamp helper used by theme generation (same as Linear ColorConverter `r` export). */
export function clampChannel(value: number, min: number, max: number): number {
  return t(value, min, max);
}


