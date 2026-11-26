import 'package:flutter/material.dart';

/// Yopsa's blue-and-green identity, expressed as theme tokens.
///
/// A logo slot exists in the home screen until the supplied asset arrives; no
/// colour or size is hard-coded in feature widgets.
const Color brandBlue = Color(0xFF0B4F9C);
const Color brandGreen = Color(0xFF0E8A6B);

ThemeData buildTheme(Brightness brightness) {
  final ColorScheme scheme = ColorScheme.fromSeed(
    seedColor: brandBlue,
    brightness: brightness,
  ).copyWith(secondary: brandGreen, onSecondary: Colors.white);

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: scheme.surface,
    appBarTheme: AppBarTheme(
      backgroundColor: brandBlue,
      foregroundColor: Colors.white,
      elevation: 0,
      centerTitle: false,
      titleTextStyle: const TextStyle(
        fontSize: 20,
        fontWeight: FontWeight.w600,
        color: Colors.white,
      ),
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: scheme.outlineVariant),
      ),
    ),
    // Large tap targets and readable body text for low-cost Android phones.
    materialTapTargetSize: MaterialTapTargetSize.padded,
    textTheme: const TextTheme(
      bodyMedium: TextStyle(fontSize: 16, height: 1.45),
      bodySmall: TextStyle(fontSize: 14, height: 1.4),
    ),
  );
}
