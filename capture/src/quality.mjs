const challengePattern = /captcha|checking your browser|cloudflare|access denied|verify (?:you are|that you are) human|enable javascript/i;

export function assessQuality({ text, title, html }) {
  const cleanText = String(text ?? '').replace(/\s+/g, ' ').trim();
  const words = cleanText ? cleanText.split(' ').length : 0;
  const challenge = challengePattern.test(`${title ?? ''}\n${cleanText.slice(0, 2000)}\n${String(html ?? '').slice(0, 4000)}`);
  const reasons = [];
  if (challenge) reasons.push('challenge_detected');
  if (words < 40) reasons.push('very_short_text');
  else if (words < 200) reasons.push('short_text');
  const score = Math.max(0, Math.min(100, Math.round(words >= 500 ? 95 : words >= 200 ? 85 : words >= 40 ? 60 : 20)) - (challenge ? 50 : 0));
  return {
    status: !challenge && words >= 200 ? 'complete' : 'partial',
    score,
    reasons,
    challenge,
  };
}
