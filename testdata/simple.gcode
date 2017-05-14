G90 ; absolute positioning
G21 ; set units to millimeters
M107 ; fan off (UV off on Wanhao D7)
G28 Z0 F150 ; home
G0 Z10
G1 Z0.05 F100
M106 ; turn on UV
G4 P10000 ; wait 10 seconds
M107 ; turn off UV
G1 Z4 F100
; and so on
