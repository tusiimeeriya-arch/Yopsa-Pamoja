import React from 'react';

export default function Splash({ fade }) {
  return (
    <div className={`splash ${fade ? 'fade' : ''}`} aria-hidden={fade}>
      <div className="splash-inner">
        <img className="splash-logo" src="/brand/logo.png" alt="Yopsa Microfinance" />
        <div className="splash-org">Yopsa Microfinance</div>
        <div className="splash-product">Pamoja</div>
        <div className="splash-tag">Stronger groups. Informed decisions.</div>
        <div className="splash-bar" aria-hidden="true"><span /></div>
      </div>
    </div>
  );
}
