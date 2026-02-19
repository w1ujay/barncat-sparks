package main

import "time"

// charDits maps ASCII characters to their Morse code duration in dit units.
// Duration includes inter-element gaps within the character but NOT the
// inter-character gap (3 dits) that follows each character.
var charDits = map[byte]int{
	'A': 5, 'B': 9, 'C': 11, 'D': 7, 'E': 1, 'F': 9,
	'G': 9, 'H': 7, 'I': 3, 'J': 13, 'K': 9, 'L': 9,
	'M': 7, 'N': 5, 'O': 11, 'P': 11, 'Q': 13, 'R': 7,
	'S': 5, 'T': 3, 'U': 7, 'V': 9, 'W': 9, 'X': 11,
	'Y': 13, 'Z': 11,
	'0': 19, '1': 17, '2': 15, '3': 13, '4': 11,
	'5': 9, '6': 11, '7': 13, '8': 15, '9': 17,
	'/': 13, '?': 15, '.': 17, ',': 19, '=': 13,
	'+': 13, '-': 15, '@': 17, '!': 19,
	'a': 5, 'b': 9, 'c': 11, 'd': 7, 'e': 1, 'f': 9,
	'g': 9, 'h': 7, 'i': 3, 'j': 13, 'k': 9, 'l': 9,
	'm': 7, 'n': 5, 'o': 11, 'p': 11, 'q': 13, 'r': 7,
	's': 5, 't': 3, 'u': 7, 'v': 9, 'w': 9, 'x': 11,
	'y': 13, 'z': 11,
}

// charDuration returns the total time to send a character at the given WPM,
// including the inter-character gap (3 dit units) that follows it.
func charDuration(ch byte, wpm int) time.Duration {
	if wpm <= 0 {
		wpm = 25
	}
	if ch == ' ' {
		// Word space = 7 dits total, but 3 are from the preceding inter-char gap
		// so we add 4 extra dit units
		ditMs := 1200.0 / float64(wpm)
		return time.Duration(4*ditMs) * time.Millisecond
	}
	dits, ok := charDits[ch]
	if !ok {
		dits = 10 // Unknown char: use average
	}
	// Add 3 dit units for inter-character gap
	dits += 3
	ditMs := 1200.0 / float64(wpm)
	return time.Duration(float64(dits)*ditMs) * time.Millisecond
}
