const { useState, useEffect, useMemo, useRef } = React;

// API base URL - ändra om du kör på annan port
const API_BASE = 'http://localhost:8080/api';

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

// Färgskala för priser
const getPriceColor = (price, minPrice, maxPrice) => {
  if (price < 0) {
    const intensity = Math.min(Math.abs(price) / 50, 1);
    const blue = Math.round(100 + intensity * 155);
    return `rgb(100, 150, ${blue})`;
  } else if (price > maxPrice) {
    return 'rgb(139, 69, 19)';
  } else {
    const ratio = Math.min(price / maxPrice, 1);
    const red = Math.round(34 + ratio * 205);
    const green = Math.round(197 - ratio * 150);
    return `rgb(${red}, ${green}, 100)`;
  }
};

function BatteryScheduler() {
  const [prices, setPrices] = useState([]);
  const [consumption, setConsumption] = useState([]);
  const [schedule, setSchedule] = useState([]);
  const [cheapestQuarters, setCheapestQuarters] = useState(12);
  const [mostExpensiveQuarters, setMostExpensiveQuarters] = useState(8);
  const [minPriceScale, setMinPriceScale] = useState(-50);
  const [maxPriceScale, setMaxPriceScale] = useState(400);
  const [currentSoC, setCurrentSoC] = useState(50);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  
  const [isDragging, setIsDragging] = useState(false);
  const [draggedQuarters, setDraggedQuarters] = useState(new Set());
  const [dragMode, setDragMode] = useState(null);
  const dragStartRef = useRef(null);
  
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
    
    // Uppdatera SoC varje minut
    const interval = setInterval(async () => {
      try {
        const socRes = await fetch(`${API_BASE}/battery-soc`);
        const socData = await socRes.json();
        setCurrentSoC(socData.percentage);
      } catch (error) {
        console.error('Failed to update SoC:', error);
      }
    }, 60000);
    
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
  
  // Simulera batterinivå över tid
  const simulateBatterySoC = useMemo(() => {
    const result = [];
    let soc = currentSoC;
    
    for (let i = 0; i < prices.length; i++) {
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
      
      result.push(Number(soc.toFixed(1)));
    }
    
    return result;
  }, [schedule, currentSoC, consumption, prices]);
  
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
  
  const sortedByPrice = useMemo(() => {
    return prices.map((p, idx) => ({ ...p, idx }))
      .sort((a, b) => a.price - b.price);
  }, [prices]);
  
  const cheapestIndices = new Set(sortedByPrice.slice(0, cheapestQuarters).map(p => p.idx));
  const expensiveIndices = new Set(sortedByPrice.slice(-mostExpensiveQuarters).map(p => p.idx));
  
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
          
          <div className="grid md:grid-cols-2 gap-4 mb-4">
            <div>
              <label className="flex items-center gap-2 text-sm font-medium mb-2">
                📉 {cheapestQuarters} billigaste kvartar
              </label>
              <input 
                type="range" 
                min="0" 
                max="48" 
                value={cheapestQuarters}
                onChange={(e) => setCheapestQuarters(parseInt(e.target.value))}
                className="w-full"
              />
            </div>
            <div>
              <label className="flex items-center gap-2 text-sm font-medium mb-2">
                📈 {mostExpensiveQuarters} dyraste kvartar
              </label>
              <input 
                type="range" 
                min="0" 
                max="48" 
                value={mostExpensiveQuarters}
                onChange={(e) => setMostExpensiveQuarters(parseInt(e.target.value))}
                className="w-full"
              />
            </div>
          </div>
          
          <div className="border-t pt-4">
            <div className="flex items-center gap-2 text-sm font-medium mb-3">
              🎨 Prisfärgskala
            </div>
            <div className="grid md:grid-cols-2 gap-4">
              <div>
                <label className="text-sm mb-2 block">
                  Min (blå) till Noll (grön): {minPriceScale} öre
                </label>
                <input 
                  type="range" 
                  min="-100" 
                  max="0" 
                  value={minPriceScale}
                  onChange={(e) => setMinPriceScale(parseInt(e.target.value))}
                  className="w-full"
                />
              </div>
              <div>
                <label className="text-sm mb-2 block">
                  Noll (grön) till Max (röd): {maxPriceScale} öre
                </label>
                <input 
                  type="range" 
                  min="100" 
                  max="600" 
                  value={maxPriceScale}
                  onChange={(e) => setMaxPriceScale(parseInt(e.target.value))}
                  className="w-full"
                />
              </div>
            </div>
          </div>
        </div>
        
        {/* Visuell översikt */}
        <div className="bg-white rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold mb-4">Översikt: Pris, Förbrukning & Schema</h2>
          <div className="w-full" style={{ height: '450px' }}>
            <svg className="w-full h-full" viewBox="0 0 1200 450" preserveAspectRatio="xMidYMid meet">
              <defs>
                <clipPath id="chartArea">
                  <rect x="60" y="10" width="1120" height="300" />
                </clipPath>
              </defs>
              
              {/* Y-axel rutnät */}
              {[0, 50, 100, 150, 200, 250, 300, 350, 400].map(price => {
                const y = 310 - (price / 400) * 300;
                return (
                  <g key={price}>
                    <line x1="60" y1={y} x2="1180" y2={y} stroke="#e5e7eb" strokeWidth="1" />
                    <text x="50" y={y + 4} textAnchor="end" fontSize="12" fill="#6b7280">
                      {price}
                    </text>
                  </g>
                );
              })}
              
              {/* X-axel */}
              {Array.from({ length: 17 }).map((_, i) => {
                const hour = i * 3;
                const x = 60 + (i / 16) * 1120;
                const label = hour < 24 ? `${hour}:00` : `${hour - 24}:00`;
                const day = hour < 24 ? 'Idag' : 'Imorgon';
                return (
                  <g key={i}>
                    <line x1={x} y1="310" x2={x} y2="315" stroke="#9ca3af" strokeWidth="1" />
                    <text x={x} y="330" textAnchor="middle" fontSize="11" fill="#6b7280">
                      {label}
                    </text>
                    {(hour === 0 || hour === 24) && (
                      <text x={x} y="345" textAnchor="middle" fontSize="10" fill="#9ca3af" fontWeight="bold">
                        {day}
                      </text>
                    )}
                  </g>
                );
              })}
              
              {/* Prislinje */}
              <g clipPath="url(#chartArea)">
                {prices.map((priceData, idx) => {
                  if (idx === 0) return null;
                  const x1 = 60 + ((idx - 1) / prices.length) * 1120;
                  const x2 = 60 + (idx / prices.length) * 1120;
                  const y1 = 310 - Math.max(0, Math.min(prices[idx - 1].price, 400)) / 400 * 300;
                  const y2 = 310 - Math.max(0, Math.min(priceData.price, 400)) / 400 * 300;
                  const color = getPriceColor(priceData.price, minPriceScale, maxPriceScale);
                  
                  return (
                    <line
                      key={idx}
                      x1={x1}
                      y1={y1}
                      x2={x2}
                      y2={y2}
                      stroke={color}
                      strokeWidth="2"
                    />
                  );
                })}
                
                {/* Förbrukningslinje */}
                {consumption.map((power, idx) => {
                  if (idx === 0) return null;
                  const x1 = 60 + ((idx - 1) / consumption.length) * 1120;
                  const x2 = 60 + (idx / consumption.length) * 1120;
                  const y1 = 310 - (consumption[idx - 1] / 5) * 300;
                  const y2 = 310 - (power / 5) * 300;
                  
                  return (
                    <line
                      key={`cons-${idx}`}
                      x1={x1}
                      y1={y1}
                      x2={x2}
                      y2={y2}
                      stroke="#6366f1"
                      strokeWidth="1.5"
                      strokeDasharray="4,4"
                      opacity="0.7"
                    />
                  );
                })}
              </g>
              
              {/* Schema-band */}
              <g>
                {prices.map((priceData, idx) => {
                  const mode = getModeForQuarter(idx);
                  const x = 60 + (idx / prices.length) * 1120;
                  const width = 1120 / prices.length;
                  
                  let fillColor = '#9ca3af';
                  if (mode === 2) fillColor = '#22c55e';
                  else if (mode === 3) fillColor = '#f97316';
                  else if (mode === 4) fillColor = '#ef4444';
                  else if (mode === 5) fillColor = '#3b82f6';
                  else if (mode === 6) fillColor = '#a855f7';
                  
                  return (
                    <rect
                      key={idx}
                      x={x}
                      y="320"
                      width={width}
                      height="30"
                      fill={fillColor}
                      opacity="0.8"
                    />
                  );
                })}
                
                {/* Legend */}
                <text x="60" y="375" fontSize="12" fill="#6b7280" fontWeight="bold">Pris</text>
                <line x1="90" y1="372" x2="120" y2="372" stroke="#22c55e" strokeWidth="2" />
                
                <text x="140" y="375" fontSize="12" fill="#6b7280" fontWeight="bold">Förbrukning (kW)</text>
                <line x1="230" y1="372" x2="260" y2="372" stroke="#6366f1" strokeWidth="2" strokeDasharray="4,4" />
                
                {MODES.slice(0, 4).map((mode, idx) => {
                  let fillColor = '#9ca3af';
                  if (mode.id === 2) fillColor = '#22c55e';
                  else if (mode.id === 3) fillColor = '#f97316';
                  else if (mode.id === 4) fillColor = '#ef4444';
                  
                  const x = 60 + idx * 120;
                  return (
                    <g key={mode.id}>
                      <rect x={x} y="395" width="20" height="20" fill={fillColor} opacity="0.8" />
                      <text x={x + 25} y="410" fontSize="11" fill="#374151">
                        {mode.name}
                      </text>
                    </g>
                  );
                })}
                
                {MODES.slice(4).map((mode, idx) => {
                  let fillColor = mode.id === 5 ? '#3b82f6' : '#a855f7';
                  const x = 540 + idx * 120;
                  return (
                    <g key={mode.id}>
                      <rect x={x} y="395" width="20" height="20" fill={fillColor} opacity="0.8" />
                      <text x={x + 25} y="410" fontSize="11" fill="#374151">
                        {mode.name}
                      </text>
                    </g>
                  );
                })}
              </g>
              
              <text x="20" y="160" textAnchor="middle" fontSize="12" fill="#6b7280" transform="rotate(-90 20 160)">
                Pris (öre/kWh) / Effekt (kW)
              </text>
              <text x="620" y="445" textAnchor="middle" fontSize="12" fill="#6b7280">
                Tid
              </text>
            </svg>
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
                  const currentMode = getModeForQuarter(idx);
                  const isCheap = cheapestIndices.has(idx);
                  const isExpensive = expensiveIndices.has(idx);
                  const isFirstOfDay = idx === 0 || idx === 96;
                  
                  const priceColor = getPriceColor(priceData.price, minPriceScale, maxPriceScale);
                  const soc = simulateBatterySoC[idx];
                  const socColor = soc <= 20 ? 'bg-red-100' : soc <= 50 ? 'bg-yellow-100' : 'bg-green-100';
                  
                  return (
                    <tr key={idx} className="border-t hover:bg-gray-50">
                      <td className={`px-3 py-2 text-left font-bold border-r sticky left-0 z-10 ${isFirstOfDay ? 'text-lg bg-white' : 'text-xs text-gray-400 bg-gray-50'}`}>
                        {formatDate(priceData.timestamp)}
                      </td>
                      <td className="px-3 py-2 text-left font-mono border-r text-xs">
                        {formatTime(priceData.timestamp)}
                      </td>
                      <td 
                        className="px-3 py-2 text-center font-semibold border-r relative text-xs"
                        style={{ backgroundColor: priceColor, color: priceData.price < 0 || priceData.price > 200 ? 'white' : 'black' }}
                      >
                        {priceData.price}
                        {isCheap && <span className="absolute top-0 right-0 text-xs">💰</span>}
                        {isExpensive && <span className="absolute top-0 right-0 text-xs">🔥</span>}
                      </td>
                      <td className="px-2 py-2 text-center border-r text-xs">
                        {consumption[idx] ? consumption[idx].toFixed(1) : '-'}
                      </td>
                      <td className={`px-2 py-2 text-center font-semibold border-r text-xs ${socColor}`}>
                        {soc}%
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