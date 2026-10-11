DELETE FROM charging_stations WHERE (name, location, connector_types, power_kw, is_operational, source) IN

-- BatteryFly — сеть быстрых станций
('BatteryFly ЭЗС №73, ул. Свердлова 11', ST_MakePoint(27.5556, 53.9006)::geography,
 ARRAY['GB/T DC', 'CCS Combo 2'], 160, true, 'manual'),

('BatteryFly, пер. Корженевского 32 (Курасовщина)', ST_MakePoint(27.5167, 53.8494)::geography,
 ARRAY['CCS Combo 2'], 120, true, 'manual'),

('BatteryFly, ул. Голубева (Газеты Правда)', ST_MakePoint(27.4794, 53.9214)::geography,
 ARRAY['GB/T DC', 'CCS Combo 2'], 160, true, 'manual'),

('BatteryFly, ул. Лейтенанта Кижеватова 32', ST_MakePoint(27.5306, 53.8744)::geography,
 ARRAY['CCS Combo 2'], 80, true, 'manual'),

('BatteryFly, ул. Натуралистов 3', ST_MakePoint(27.6014, 53.9278)::geography,
 ARRAY['CCS Combo 2'], 80, true, 'manual'),

('BatteryFly, просп. Жукова 29', ST_MakePoint(27.4964, 53.9425)::geography,
 ARRAY['CCS Combo 2'], 80, true, 'manual'),

-- Malanka — сеть станций разной мощности
('Malanka, просп. Независимости 3/2', ST_MakePoint(27.5619, 53.9006)::geography,
 ARRAY['CHAdeMO', 'GB/T DC', 'CCS Combo 2'], 50, true, 'manual'),

('Malanka, исторический район Замчище', ST_MakePoint(27.5511, 53.9072)::geography,
 ARRAY['Type 2'], 22, true, 'manual'),

('Malanka, Коллекторная ул. 3А', ST_MakePoint(27.5836, 53.8981)::geography,
 ARRAY['CHAdeMO', 'CCS Combo 2'], 50, true, 'manual'),

-- Зарядка (оператор "Зарядка")
('Зарядка ЭЗС №30, ул. Мельникайте 16а', ST_MakePoint(27.6083, 53.9314)::geography,
 ARRAY['GB/T DC', 'CCS Combo 2'], 80, true, 'manual'),

('Зарядка, ул. Ленина 27', ST_MakePoint(27.5567, 53.9003)::geography,
 ARRAY['CHAdeMO', 'CCS Combo 2'], 120, true, 'manual'),

-- Zaryadka — хабы по городу
('Zaryadka, Игуменский тракт 22', ST_MakePoint(27.6428, 53.9186)::geography,
 ARRAY['Type 2', 'CCS Combo 2'], 60, true, 'manual'),

('Zaryadka, пр. Дзержинского 104', ST_MakePoint(27.4739, 53.8814)::geography,
 ARRAY['Type 2', 'CCS Combo 2'], 60, true, 'manual'),

('Zaryadka, пр. Партизанский 178', ST_MakePoint(27.5981, 53.8408)::geography,
 ARRAY['Type 2', 'CCS Combo 2'], 60, true, 'manual'),

-- United Company — ЭЗК с более крупной инфраструктурой (АЗС-хаб)
('United Company ЭЗК №8, просп. Победителей 102', ST_MakePoint(27.5297, 53.9364)::geography,
 ARRAY['GB/T DC', 'CCS Combo 2', 'CHAdeMO'], 150, true, 'manual');