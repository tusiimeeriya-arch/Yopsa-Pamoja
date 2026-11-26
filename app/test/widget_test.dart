import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pamoja/src/app.dart';

void main() {
  testWidgets('the home shell shows the product name and tagline', (WidgetTester tester) async {
    await tester.pumpWidget(const ProviderScope(child: PamojaApp()));
    await tester.pumpAndSettle();

    expect(find.text('Yopsa Pamoja'), findsOneWidget);
    expect(find.text('Stronger groups. Informed decisions.'), findsOneWidget);
  });
}
