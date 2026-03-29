const { useState, useEffect, useMemo, useRef } = React;

// API base URL - ändra om du kör på annan port
const API_BASE = '/api';

const MODES = [
  { id: 1, name: 'Passiv', color: 'bg-gray-400', textColor: 'text-gray-800', desc: 'Sol' },
  { id: 2, name: 'Ladda', color: 'bg-green-500', textColor: 'text-white', desc: 'Nät→Batteri' },
  { id: 3, name: 'Urladda', color: 'bg-orange-500', textColor: 'text-white', desc: 'Batteri→Hus' },
  { id: 4, name: 'Effekt', color: 'bg-red-500', textColor: 'text-white', desc: 'Begränsa' },
  { id: 5, name: 'Laddbox G', color: 'bg-blue-500', textColor: 'text-white', desc: 'Garage' },
  { id: 6, name: 'Laddbox U', color: 'bg-purple-500', textColor: 'text-white', desc: 'Ute' }
];

const BATTERY_CAPACITY = 42;
const MIN_SOC = 15;

// Laddkurva approximerad från bilden
const getChargePower = (soc) => {
  if (soc < 15) return 0;
  if (soc <= 80) return -10.0;
  if (soc <= 90) return -10.0 + (soc - 80) * (5.0 / 10);
  if (soc <= 95) return -5.0 + (soc - 90) * (3.0 / 5);
  return -2.0;
};


function BatteryScheduler() {
  const [prices, setPrices] = useState([]);
  const [consumption, setConsumption] = useState([]);
  const [schedule, setSchedule] = useState([]);
  const [priceYMin, setPriceYMin] = useState(0);
  const [priceYMax, setPriceYMax] = useState(400);
  const [priceDiffD, setPriceDiffD] = useState(50);
  const [currentSoC, setCurrentSoC] = useState(50);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  
  const [isDragging, setIsDragging] = useState(false);
  const [draggedQuarters, setDraggedQuarters] = useState(new Set());
  const [dragMode, setDragMode] = useState(null);
  const dragStartRef = useRef(null);
  const [hoverIdx, setHoverIdx] = useState(null);
  const svgRef = useRef(null);
  
  // Ladda data från backend
  useEffect(() => {
    const loadData = async () => {
      try {
        // Hämta priser
        const pricesRes = await fetch(`${API_BASE}/prices`);
        const pricesData = await pricesRes.json();
        setPrices(pricesData.map(p => ({
          ...p,
          timestamp: new Date(p.timestamp)
        })));
        
        // Hämta schedule
        const scheduleRes = await fetch(`${API_BASE}/schedule`);
        const scheduleData = await scheduleRes.json();
        setSchedule(scheduleData.map(s => ({
          ...s,
          timestamp: new Date(s.timestamp)
        })));
        
        // Hämta förbrukning
        const consumptionRes = await fetch(`${API_BASE}/power-estimate`);
        const consumptionData = await consumptionRes.json();
        setConsumption(consumptionData.map(c => c.power_kw));
        
        // Hämta batterinivå
        const socRes = await fetch(`${API_BASE}/battery-soc`);
        const socData = await socRes.json();
        setCurrentSoC(socData.percentage);
        
        setLoading(false);
      } catch (error) {
        console.error('Failed to load data:', error);
        alert('Kunde inte ladda data från servern');
        setLoading(false);
      }
    };
    
    loadData();
    
    // Uppdatera SoC var 30:e sekund (fångar solpanelsladdning snabbare)
    const interval = setInterval(async () => {
      try {
        const socRes = await fetch(`${API_BASE}/battery-soc`);
        const socData = await socRes.json();
        setCurrentSoC(socData.percentage);
      } catch (error) {
        console.error('Failed to update SoC:', error);
      }
    }, 30000);
    
    return () => clearInterval(interval);
  }, []);
  
  const getModeForQuarter = (quarterIndex) => {
    if (!prices[quarterIndex]) return 1;
    const quarterTime = prices[quarterIndex].timestamp;
    let currentMode = 1;
    for (const change of schedule) {
      if (change.timestamp <= quarterTime) {
        currentMode = change.mode;
      } else {
        break;
      }
    }
    return currentMode;
  };
  
  // Hitta index för aktuell kvart
  const currentQuarterIndex = useMemo(() => {
    if (prices.length === 0) return 0;
    const now = new Date();
    for (let i = 0; i < prices.length; i++) {
      const nextTime = i + 1 < prices.length ? prices[i + 1].timestamp : new Date(prices[i].timestamp.getTime() + 15 * 60 * 1000);
      if (now >= prices[i].timestamp && now < nextTime) return i;
    }
    return 0;
  }, [prices]);

  // Simulera batterinivå: använd riktig SoC vid aktuell tidpunkt, simulera framåt
  const simulateBatterySoC = useMemo(() => {
    const result = new Array(prices.length).fill(null);
    if (prices.length === 0) return result;

    // Sätt riktig SoC vid aktuell kvart
    result[currentQuarterIndex] = currentSoC;

    // Simulera framåt från nu
    let soc = currentSoC;
    for (let i = currentQuarterIndex + 1; i < prices.length; i++) {
      const mode = getModeForQuarter(i);
      const consumptionKw = consumption[i] || 1.0;
      const quarterHours = 0.25;

      let deltaEnergy = 0;

      if (mode === 2) {
        const chargePower = getChargePower(soc);
        deltaEnergy = chargePower * quarterHours;
      } else if (mode === 3) {
        deltaEnergy = consumptionKw * quarterHours;
      } else if (mode === 5 || mode === 6) {
        deltaEnergy = 0;
      }

      const deltaSoC = (deltaEnergy / BATTERY_CAPACITY) * 100;
      soc = Math.max(MIN_SOC, Math.min(100, soc - deltaSoC));

      result[i] = Number(soc.toFixed(1));
    }

    return result;
  }, [schedule, currentSoC, consumption, prices, currentQuarterIndex]);
  
  // Summering
  const summary = useMemo(() => {
    const counts = { 1: 0, 2: 0, 3: 0, 4: 0, 5: 0, 6: 0 };
    for (let i = 0; i < prices.length; i++) {
      const mode = getModeForQuarter(i);
      counts[mode]++;
    }
    return {
      1: (counts[1] / 4).toFixed(2),
      2: (counts[2] / 4).toFixed(2),
      3: (counts[3] / 4).toFixed(2),
      4: (counts[4] / 4).toFixed(2),
      5: (counts[5] / 4).toFixed(2),
      6: (counts[6] / 4).toFixed(2)
    };
  }, [schedule, prices]);
  
  // Blocks
  const blocks = useMemo(() => {
    const result = [];
    let currentMode = null;
    let blockStart = null;
    
    for (let i = 0; i < prices.length; i++) {
      const mode = getModeForQuarter(i);
      
      if (mode !== currentMode) {
        if (currentMode !== null && blockStart !== null) {
          const blockEnd = prices[i].timestamp;
          const durationMinutes = (blockEnd - blockStart) / (1000 * 60);
          const durationHours = (durationMinutes / 60).toFixed(2);
          
          result.push({
            mode: currentMode,
            start: blockStart,
            end: blockEnd,
            duration: durationHours
          });
        }
        
        currentMode = mode;
        blockStart = prices[i].timestamp;
      }
    }
    
    if (currentMode !== null && blockStart !== null) {
      const blockEnd = new Date(prices[prices.length - 1].timestamp.getTime() + 15 * 60 * 1000);
      const durationMinutes = (blockEnd - blockStart) / (1000 * 60);
      const durationHours = (durationMinutes / 60).toFixed(2);
      
      result.push({
        mode: currentMode,
        start: blockStart,
        end: blockEnd,
        duration: durationHours
      });
    }
    
    return result;
  }, [schedule, prices]);
  
  // Beräkna laddnings- och urladdningskvartar baserat på prisdifferens D
  const { chargeIndices, dischargeIndices } = useMemo(() => {
    const sorted = prices
      .map((p, idx) => ({ price: p.price, idx }))
      .filter(p => p.idx >= currentQuarterIndex)
      .sort((a, b) => a.price - b.price);

    const charge = new Set();
    const discharge = new Set();

    let lo = 0, hi = sorted.length - 1;
    while (lo < hi && sorted[hi].price - sorted[lo].price >= priceDiffD) {
      charge.add(sorted[lo].idx);
      discharge.add(sorted[hi].idx);
      lo++;
      hi--;
    }

    return { chargeIndices: charge, dischargeIndices: discharge };
  }, [prices, currentQuarterIndex, priceDiffD]);
  
  const validateChargerOverlap = (startIdx, endIdx, mode) => {
    if (mode !== 5 && mode !== 6) return null;
    
    for (let i = 0; i < prices.length; i++) {
      if (i >= startIdx && i <= endIdx) continue;
      
      const otherMode = getModeForQuarter(i);
      if (otherMode === 5 || otherMode === 6) {
        const otherTime = prices[i].timestamp;
        const ourStartTime = prices[startIdx].timestamp;
        const ourEndTime = prices[endIdx].timestamp;
        
        if (otherTime >= ourStartTime && otherTime <= ourEndTime) {
          return `Det finns redan en annan laddbox aktiv under denna tid!`;
        }
      }
    }
    return null;
  };
  
  const handleMouseDown = (quarterIndex, mode) => {
    const currentMode = getModeForQuarter(quarterIndex);
    
    if (mode === 1 && (currentMode === 5 || currentMode === 6)) {
      mode = 1;
    } else if ((mode === 5 || mode === 6) && currentMode === mode) {
      mode = 1;
    }
    
    setIsDragging(true);
    setDragMode(mode);
    setDraggedQuarters(new Set([quarterIndex]));
    dragStartRef.current = quarterIndex;
  };
  
  const handleMouseEnter = (quarterIndex) => {
    if (isDragging) {
      setDraggedQuarters(prev => new Set([...prev, quarterIndex]));
    }
  };
  
  const handleMouseUp = () => {
    if (isDragging && draggedQuarters.size > 0) {
      const sortedQuarters = Array.from(draggedQuarters).sort((a, b) => a - b);
      
      const error = validateChargerOverlap(sortedQuarters[0], sortedQuarters[sortedQuarters.length - 1], dragMode);
      if (error) {
        alert(error);
        setIsDragging(false);
        setDraggedQuarters(new Set());
        setDragMode(null);
        dragStartRef.current = null;
        return;
      }
      
      if (sortedQuarters.length === 1) {
        const quarterIndex = sortedQuarters[0];
        const clickedTime = prices[quarterIndex].timestamp;
        const nextTime = new Date(clickedTime.getTime() + 15 * 60 * 1000);
        
        let newSchedule = schedule.filter(s => 
          s.timestamp < clickedTime || s.timestamp >= nextTime
        );
        
        newSchedule.push({ timestamp: clickedTime, mode: dragMode });
        newSchedule.push({ timestamp: nextTime, mode: 1 });
        
        newSchedule.sort((a, b) => a.timestamp - b.timestamp);
        setSchedule(newSchedule);
      } else {
        const firstQuarter = sortedQuarters[0];
        const lastQuarter = sortedQuarters[sortedQuarters.length - 1];
        
        const firstTime = prices[firstQuarter].timestamp;
        const lastTime = prices[lastQuarter].timestamp;
        const nextTime = new Date(lastTime.getTime() + 15 * 60 * 1000);
        
        let newSchedule = schedule.filter(s => 
          s.timestamp < firstTime || s.timestamp >= nextTime
        );
        
        newSchedule.push({ timestamp: firstTime, mode: dragMode });
        newSchedule.push({ timestamp: nextTime, mode: 1 });
        
        newSchedule.sort((a, b) => a.timestamp - b.timestamp);
        setSchedule(newSchedule);
      }
    }
    
    setIsDragging(false);
    setDraggedQuarters(new Set());
    setDragMode(null);
    dragStartRef.current = null;
  };
  
  const handleAutoFill = () => {
    if (chargeIndices.size === 0 && dischargeIndices.size === 0) {
      alert('Inga kvartar att fylla i. Justera prisdifferensen (D).');
      return;
    }

    // Bygg en modkarta för varje kvart från nu och framåt
    const modeMap = new Map();
    for (let i = currentQuarterIndex; i < prices.length; i++) {
      if (chargeIndices.has(i)) {
        modeMap.set(i, 2); // Ladda
      } else if (dischargeIndices.has(i)) {
        modeMap.set(i, 3); // Urladda
      } else {
        modeMap.set(i, 1); // Passiv
      }
    }

    // Konvertera till breakpoints (bara vid lägesändringar)
    const newSchedule = [];
    let prevMode = null;
    for (let i = currentQuarterIndex; i < prices.length; i++) {
      const mode = modeMap.get(i);
      if (mode !== prevMode) {
        newSchedule.push({ timestamp: prices[i].timestamp, mode });
        prevMode = mode;
      }
    }

    setSchedule(newSchedule);
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      const response = await fetch(`${API_BASE}/schedule`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(schedule.map(s => ({
          timestamp: s.timestamp.toISOString(),
          mode: s.mode
        })))
      });
      
      if (!response.ok) {
        throw new Error('Failed to save schedule');
      }
      
      alert('Schema sparat!');
    } catch (error) {
      console.error('Failed to save:', error);
      alert('Kunde inte spara schema');
    } finally {
      setSaving(false);
    }
  };
  
  const formatTime = (date) => {
    return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  };
  
  const formatDate = (date) => {
    const today = new Date();
    const tomorrow = new Date(today);
    tomorrow.setDate(tomorrow.getDate() + 1);
    
    if (date.getDate() === today.getDate()) return 'Idag';
    if (date.getDate() === tomorrow.getDate()) return 'Imorgon';
    return date.toLocaleDateString('sv-SE');
  };
  
  if (loading) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center">
        <div className="text-center">
          <div className="text-2xl font-bold mb-4">Laddar...</div>
          <div className="text-gray-600">Hämtar data från servern</div>
        </div>
      </div>
    );
  }
  
  return (
    <div className="min-h-screen bg-gray-50 p-4" onMouseUp={handleMouseUp}>
      <div className="max-w-7xl mx-auto space-y-4">
        <div className="bg-white rounded-lg shadow p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="text-2xl">🔋</div>
            <h1 className="text-2xl font-bold text-gray-800">Batteristyrning SE3</h1>
            <div className="ml-auto text-right">
              <div className="text-sm text-gray-600">Aktuell laddnivå</div>
              <div className="text-2xl font-bold text-blue-600">{currentSoC}%</div>
            </div>
          </div>
          
          <div className="grid md:grid-cols-3 gap-4">
            <div>
              <label className="text-sm font-medium mb-2 block">
                Prisskala: {priceYMin} - {priceYMax} öre
              </label>
              <div className="flex gap-2 items-center">
                <input
                  type="range" min="-50" max="500" step="50"
                  value={priceYMin}
                  onChange={(e) => {
                    const v = parseInt(e.target.value);
                    if (v < priceYMax) setPriceYMin(v);
                  }}
                  className="w-full"
                />
                <input
                  type="range" min="50" max="1000" step="50"
                  value={priceYMax}
                  onChange={(e) => {
                    const v = parseInt(e.target.value);
                    if (v > priceYMin) setPriceYMax(v);
                  }}
                  className="w-full"
                />
              </div>
            </div>
            <div>
              <label className="text-sm font-medium mb-2 block">
                Prisdifferens (D): {priceDiffD} öre
              </label>
              <input
                type="range" min="0" max="300" step="5"
                value={priceDiffD}
                onChange={(e) => setPriceDiffD(parseInt(e.target.value))}
                className="w-full"
              />
              <div className="text-xs text-gray-500 mt-1">
                {chargeIndices.size} kvartar laddning / {dischargeIndices.size} kvartar urladdning
              </div>
            </div>
          </div>
        </div>
        
        {/* Visuell översikt */}
        <div className="bg-white rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold mb-4">Översikt: Pris, Förbrukning, SoC & Schema</h2>
          {(() => {
            const cStart = currentQuarterIndex;
            const cLen = prices.length - cStart;
            if (cLen <= 0) return null;
            const toX = (idx) => 60 + ((idx - cStart) / cLen) * 1120;

            const handleChartMouseMove = (e) => {
              const svg = svgRef.current;
              if (!svg) return;
              const rect = svg.getBoundingClientRect();
              const svgX = (e.clientX - rect.left) / rect.width * 1260;
              if (svgX < 60 || svgX > 1180) { setHoverIdx(null); return; }
              const ratio = (svgX - 60) / 1120;
              const idx = cStart + Math.floor(ratio * cLen);
              if (idx >= cStart && idx < prices.length) {
                setHoverIdx(idx);
              } else {
                setHoverIdx(null);
              }
            };

            // X-axel: generera timmarker från nuvarande timme framåt
            const startHour = prices[cStart].timestamp.getHours();
            const startDay = prices[cStart].timestamp.getDate();
            const xLabels = [];
            for (let i = cStart; i < prices.length; i++) {
              const h = prices[i].timestamp.getHours();
              const m = prices[i].timestamp.getMinutes();
              if (m === 0 && h % 3 === 0) {
                const isNewDay = prices[i].timestamp.getDate() !== startDay && h === 0;
                xLabels.push({ x: toX(i), label: `${h}:00`, isNewDay, date: prices[i].timestamp });
              }
            }

            return (
            <div className="w-full" style={{ height: '560px', position: 'relative' }}>
              <svg ref={svgRef} className="w-full h-full" viewBox="0 0 1260 560" preserveAspectRatio="xMidYMid meet"
                onMouseMove={handleChartMouseMove}
                onMouseLeave={() => setHoverIdx(null)}
              >
                <defs>
                  <clipPath id="chartArea">
                    <rect x="60" y="10" width="1120" height="300" />
                  </clipPath>
                </defs>

                {/* Y-axel vänster: Pris (öre/kWh), dynamisk skala */}
                {(() => {
                  const range = priceYMax - priceYMin;
                  const step = range <= 200 ? 25 : range <= 500 ? 50 : 100;
                  const ticks = [];
                  for (let p = Math.ceil(priceYMin / step) * step; p <= priceYMax; p += step) {
                    ticks.push(p);
                  }
                  return ticks.map(price => {
                    const y = 310 - ((price - priceYMin) / (priceYMax - priceYMin)) * 300;
                    return (
                      <g key={price}>
                        <line x1="60" y1={y} x2="1180" y2={y} stroke="#e5e7eb" strokeWidth="1" />
                        <text x="50" y={y + 4} textAnchor="end" fontSize="12" fill="#6b7280">
                          {price}
                        </text>
                      </g>
                    );
                  });
                })()}

                {/* Y-axel höger: kW (0-10) / SoC% (0-100) */}
                {[0, 2, 4, 6, 8, 10].map(kw => {
                  const y = 310 - (kw / 10) * 300;
                  const socPct = kw * 10;
                  return (
                    <g key={`right-${kw}`}>
                      <text x="1190" y={y + 4} textAnchor="start" fontSize="11" fill="#6366f1">
                        {kw}
                      </text>
                      <text x="1235" y={y + 4} textAnchor="start" fontSize="11" fill="#f59e0b">
                        {socPct}%
                      </text>
                    </g>
                  );
                })}

                {/* X-axel */}
                {xLabels.map((lbl, i) => (
                  <g key={i}>
                    <line x1={lbl.x} y1="310" x2={lbl.x} y2="315" stroke="#9ca3af" strokeWidth="1" />
                    <text x={lbl.x} y="330" textAnchor="middle" fontSize="11" fill="#6b7280">
                      {lbl.label}
                    </text>
                    {lbl.isNewDay && (
                      <text x={lbl.x} y="345" textAnchor="middle" fontSize="10" fill="#9ca3af" fontWeight="bold">
                        {formatDate(lbl.date)}
                      </text>
                    )}
                  </g>
                ))}

                {/* Ladda/Urladda-bakgrund + Prislinje (steg) */}
                <g clipPath="url(#chartArea)">
                  {/* Bakgrundsfärg för ladda/urladda-kvartar */}
                  {prices.map((priceData, idx) => {
                    if (idx < cStart) return null;
                    const isCharge = chargeIndices.has(idx);
                    const isDischarge = dischargeIndices.has(idx);
                    if (!isCharge && !isDischarge) return null;
                    const x = toX(idx);
                    const w = 1120 / cLen;
                    return (
                      <rect key={`bg-${idx}`} x={x} y="10" width={w} height="300"
                        fill={isCharge ? '#22c55e' : '#f97316'} opacity="0.15" />
                    );
                  })}

                  {/* Prislinje som stegfunktion */}
                  {prices.map((priceData, idx) => {
                    if (idx < cStart) return null;
                    const x1 = toX(idx);
                    const x2 = idx + 1 < prices.length ? toX(idx + 1) : toX(idx) + 1120 / cLen;
                    const yRange = priceYMax - priceYMin;
                    const y = 310 - ((Math.max(priceYMin, Math.min(priceYMax, priceData.price)) - priceYMin) / yRange) * 300;

                    return (
                      <line key={`price-${idx}`} x1={x1} y1={y} x2={x2} y2={y} stroke="#374151" strokeWidth="2" />
                    );
                  })}

                  {/* Förbrukningslinje (skala 0-10 kW) */}
                  {consumption.map((power, idx) => {
                    if (idx <= cStart || !consumption[idx - 1]) return null;
                    const x1 = toX(idx - 1);
                    const x2 = toX(idx);
                    const y1 = 310 - (Math.min(consumption[idx - 1], 10) / 10) * 300;
                    const y2 = 310 - (Math.min(power, 10) / 10) * 300;

                    return (
                      <line key={`cons-${idx}`} x1={x1} y1={y1} x2={x2} y2={y2}
                        stroke="#6366f1" strokeWidth="1.5" strokeDasharray="4,4" opacity="0.7" />
                    );
                  })}

                  {/* SoC-linje (skala 0-100%) */}
                  {simulateBatterySoC.map((soc, idx) => {
                    if (idx <= cStart || soc === null || simulateBatterySoC[idx - 1] === null) return null;
                    const x1 = toX(idx - 1);
                    const x2 = toX(idx);
                    const y1 = 310 - (simulateBatterySoC[idx - 1] / 100) * 300;
                    const y2 = 310 - (soc / 100) * 300;

                    return (
                      <line key={`soc-${idx}`} x1={x1} y1={y1} x2={x2} y2={y2}
                        stroke="#f59e0b" strokeWidth="2" opacity="0.8" />
                    );
                  })}
                </g>

                {/* Klickbara lägesrader */}
                {(() => {
                  const modeColors = { 1: '#9ca3af', 2: '#22c55e', 3: '#f97316', 4: '#ef4444', 5: '#3b82f6', 6: '#a855f7' };
                  const rowH = 20;
                  const rowGap = 1;
                  const rowsY = 325;
                  const w = 1120 / cLen;

                  return (
                    <g>
                      {MODES.map((mode, modeRow) => {
                        const rowY = rowsY + modeRow * (rowH + rowGap);
                        return (
                          <g key={`moderow-${mode.id}`}>
                            {/* Lägesnamn */}
                            <text x="55" y={rowY + 14} textAnchor="end" fontSize="10" fill="#6b7280">
                              {mode.name}
                            </text>
                            {/* Kvartar */}
                            {prices.map((priceData, idx) => {
                              if (idx < cStart) return null;
                              const x = toX(idx);
                              const activeMode = getModeForQuarter(idx);
                              const isActive = activeMode === mode.id;
                              const isDragTarget = draggedQuarters.has(idx) && dragMode === mode.id;

                              return (
                                <rect
                                  key={`mr-${mode.id}-${idx}`}
                                  x={x} y={rowY} width={w} height={rowH}
                                  fill={isActive || isDragTarget ? modeColors[mode.id] : '#f3f4f6'}
                                  opacity={isActive || isDragTarget ? 0.9 : 0.4}
                                  stroke={isDragTarget ? '#3b82f6' : 'white'}
                                  strokeWidth={isDragTarget ? 1.5 : 0.5}
                                  style={{ cursor: 'pointer' }}
                                  onMouseDown={(e) => { e.preventDefault(); handleMouseDown(idx, mode.id); }}
                                  onMouseEnter={() => handleMouseEnter(idx)}
                                />
                              );
                            })}
                          </g>
                        );
                      })}
                    </g>
                  );
                })()}

                {/* Hover-linje och tooltip */}
                {hoverIdx !== null && hoverIdx >= cStart && hoverIdx < prices.length && (
                  <g>
                    <line x1={toX(hoverIdx)} y1="10" x2={toX(hoverIdx)} y2="310" stroke="#374151" strokeWidth="1" strokeDasharray="3,3" />
                    <rect
                      x={Math.min(toX(hoverIdx) + 8, 1060)}
                      y="15"
                      width="180" height="80" rx="4"
                      fill="white" stroke="#d1d5db" strokeWidth="1"
                      filter="drop-shadow(0 1px 2px rgba(0,0,0,0.1))"
                    />
                    <text x={Math.min(toX(hoverIdx) + 18, 1070)} y="33" fontSize="12" fontWeight="bold" fill="#374151">
                      {formatDate(prices[hoverIdx].timestamp)} {formatTime(prices[hoverIdx].timestamp)}
                    </text>
                    <text x={Math.min(toX(hoverIdx) + 18, 1070)} y="51" fontSize="12" fill="#6b7280">
                      Pris: {prices[hoverIdx].price} öre/kWh
                    </text>
                    <text x={Math.min(toX(hoverIdx) + 18, 1070)} y="67" fontSize="12" fill="#6366f1">
                      Förbrukning: {consumption[hoverIdx] ? consumption[hoverIdx].toFixed(1) : '-'} kW
                    </text>
                    <text x={Math.min(toX(hoverIdx) + 18, 1070)} y="83" fontSize="12" fill="#f59e0b">
                      SoC: {simulateBatterySoC[hoverIdx] !== null ? `${simulateBatterySoC[hoverIdx]}%` : '-'}
                    </text>
                  </g>
                )}

                {/* Legend */}
                <g>
                  <text x="60" y="470" fontSize="12" fill="#374151" fontWeight="bold">Pris</text>
                  <line x1="90" y1="467" x2="120" y2="467" stroke="#374151" strokeWidth="2" />

                  <text x="140" y="470" fontSize="12" fill="#6366f1" fontWeight="bold">Förbrukning</text>
                  <line x1="225" y1="467" x2="255" y2="467" stroke="#6366f1" strokeWidth="2" strokeDasharray="4,4" />

                  <text x="275" y="470" fontSize="12" fill="#f59e0b" fontWeight="bold">SoC</text>
                  <line x1="300" y1="467" x2="330" y2="467" stroke="#f59e0b" strokeWidth="2" />

                  <rect x="350" y="458" width="20" height="16" fill="#22c55e" opacity="0.3" />
                  <text x="375" y="470" fontSize="12" fill="#374151">Ladda</text>

                  <rect x="420" y="458" width="20" height="16" fill="#f97316" opacity="0.3" />
                  <text x="445" y="470" fontSize="12" fill="#374151">Urladda</text>
                </g>

                <text x="20" y="160" textAnchor="middle" fontSize="12" fill="#6b7280" transform="rotate(-90 20 160)">
                  Pris (öre/kWh)
                </text>
                <text x="1210" y="100" textAnchor="middle" fontSize="11" fill="#6366f1" transform="rotate(90 1210 100)">
                  kW
                </text>
                <text x="1250" y="100" textAnchor="middle" fontSize="11" fill="#f59e0b" transform="rotate(90 1250 100)">
                  SoC%
                </text>
              </svg>
            </div>
            );
          })()}
          {/* Auto-fill knapp */}
          <div className="mt-3 flex gap-3">
            <button
              onClick={handleAutoFill}
              className="px-4 py-2 bg-indigo-600 hover:bg-indigo-700 text-white rounded-lg text-sm font-medium transition-colors"
            >
              Fyll i schema (D={priceDiffD} öre: {chargeIndices.size} ladda / {dischargeIndices.size} urladda)
            </button>
          </div>
        </div>

        {/* Summering per läge */}
        <div className="bg-white rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold mb-4">Summering per läge</h2>
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4">
            {MODES.map(mode => (
              <div key={mode.id} className="flex items-center gap-2">
                <div className={`w-6 h-6 ${mode.color} rounded`}></div>
                <div>
                  <div className="font-medium text-sm">{mode.name}</div>
                  <div className="font-bold">{summary[mode.id]}h</div>
                </div>
              </div>
            ))}
          </div>
        </div>
        
        {/* Block-översikt */}
        <div className="bg-white rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold mb-4">Tidslinje ({blocks.length} block)</h2>
          <div className="space-y-2 max-h-96 overflow-y-auto">
            {blocks.map((block, idx) => {
              const mode = MODES.find(m => m.id === block.mode);
              const startTime = formatTime(block.start);
              const endTime = formatTime(block.end);
              const startDate = formatDate(block.start);
              
              return (
                <div key={idx} className="flex items-center gap-3 p-2 hover:bg-gray-50 rounded">
                  <div className={`w-8 h-8 ${mode.color} rounded flex-shrink-0`}></div>
                  <div className="flex-1 min-w-0">
                    <div className="font-medium">{mode.name}</div>
                    <div className="text-sm text-gray-600 truncate">
                      {startDate} {startTime} - {endTime}
                    </div>
                  </div>
                  <div className="font-bold text-lg text-gray-700 flex-shrink-0">
                    {block.duration}h
                  </div>
                </div>
              );
            })}
          </div>
        </div>
        
        {/* Schema-tabell */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="text-sm text-gray-600 p-4 border-b bg-gray-50">
            <div className="flex items-center gap-2">
              ⚠️ <strong>Tips:</strong> Klicka och dra för att markera flera kvartar. Laddbox kräver att batteriet är i Passiv-läge.
            </div>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-100 sticky top-0 z-10">
                <tr>
                  <th className="px-3 py-2 text-left font-semibold border-r sticky left-0 bg-gray-100 z-20">Dag</th>
                  <th className="px-3 py-2 text-left font-semibold border-r">Tid</th>
                  <th className="px-3 py-2 text-center font-semibold border-r">
                    Pris<br/>
                    <span className="text-xs font-normal">(öre)</span>
                  </th>
                  <th className="px-3 py-2 text-center font-semibold border-r">
                    Effekt<br/>
                    <span className="text-xs font-normal">(kW)</span>
                  </th>
                  <th className="px-3 py-2 text-center font-semibold border-r">
                    Batteri<br/>
                    <span className="text-xs font-normal">(%)</span>
                  </th>
                  {MODES.map(mode => (
                    <th key={mode.id} className="px-3 py-2 text-center font-semibold border-r">
                      <div className="text-xs">{mode.name}</div>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {prices.map((priceData, idx) => {
                  if (idx < currentQuarterIndex) return null;
                  const currentMode = getModeForQuarter(idx);
                  const isCharge = chargeIndices.has(idx);
                  const isDischarge = dischargeIndices.has(idx);
                  const isFirstOfDay = idx === 0 || idx === 96 || idx === currentQuarterIndex;

                  const soc = simulateBatterySoC[idx];
                  const socColor = soc === null ? 'bg-gray-50' : soc <= 20 ? 'bg-red-100' : soc <= 50 ? 'bg-yellow-100' : 'bg-green-100';
                  const priceBg = isCharge ? 'bg-green-100' : isDischarge ? 'bg-orange-100' : '';

                  return (
                    <tr key={idx} className="border-t hover:bg-gray-50">
                      <td className={`px-3 py-2 text-left font-bold border-r sticky left-0 z-10 ${isFirstOfDay ? 'text-lg bg-white' : 'text-xs text-gray-400 bg-gray-50'}`}>
                        {formatDate(priceData.timestamp)}
                      </td>
                      <td className="px-3 py-2 text-left font-mono border-r text-xs">
                        {formatTime(priceData.timestamp)}
                      </td>
                      <td className={`px-3 py-2 text-center font-semibold border-r text-xs ${priceBg}`}>
                        {priceData.price}
                      </td>
                      <td className="px-2 py-2 text-center border-r text-xs">
                        {consumption[idx] ? consumption[idx].toFixed(1) : '-'}
                      </td>
                      <td className={`px-2 py-2 text-center font-semibold border-r text-xs ${socColor}`}>
                        {soc !== null ? `${soc}%` : '-'}
                      </td>
                      {MODES.map(mode => {
                        const isActive = currentMode === mode.id;
                        const isPassiveWhenCharging = mode.id === 1 && (currentMode === 5 || currentMode === 6);
                        const isDraggedHere = draggedQuarters.has(idx) && dragMode === mode.id;
                        
                        return (
                          <td key={mode.id} className="px-1 py-1 border-r">
                            <button
                              onMouseDown={() => handleMouseDown(idx, mode.id)}
                              onMouseEnter={() => handleMouseEnter(idx)}
                              title={`${mode.name} från ${formatTime(priceData.timestamp)}`}
                              className={`w-full h-8 rounded transition-all select-none text-xs ${
                                isDraggedHere
                                  ? `${mode.color} ${mode.textColor} font-bold shadow-lg ring-2 ring-blue-400`
                                  : (isActive || isPassiveWhenCharging)
                                    ? `${mode.color} ${mode.textColor} font-bold shadow-md` 
                                    : 'bg-gray-100 hover:bg-gray-200 text-gray-400'
                              }`}
                            >
                              {(isActive || isPassiveWhenCharging || isDraggedHere) ? '●' : '○'}
                            </button>
                          </td>
                        );
                      })}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
        
        {/* Spara-knapp */}
        <div className="bg-white rounded-lg shadow p-6">
          <button 
            onClick={handleSave}
            disabled={saving}
            className={`w-full font-semibold py-3 rounded-lg transition-colors ${
              saving 
                ? 'bg-gray-400 cursor-not-allowed' 
                : 'bg-blue-600 hover:bg-blue-700 text-white'
            }`}
          >
            {saving ? 'Sparar...' : '💾 Spara schema'}
          </button>
        </div>
      </div>
    </div>
  );
}

// Rendera appen
const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(<BatteryScheduler />);