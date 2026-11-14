package dev.voidrot.chorecast

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.core.Appearance
import dev.voidrot.chorecast.core.KidOverview
import dev.voidrot.chorecast.ui.components.SectionHeader
import dev.voidrot.chorecast.ui.home.KidCard
import dev.voidrot.chorecast.ui.kid.ChoreRow
import dev.voidrot.chorecast.ui.settings.SettingsScreen
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.time.LocalDate
import java.time.OffsetDateTime

// The real ChorecastApp builds a Keystore-backed container, which Robolectric cannot provide.
@Config(application = android.app.Application::class)
@RunWith(AndroidJUnit4::class)
class SemanticsTest {
    @get:Rule
    val rule = createComposeRule()

    private val kid = Kid(color = "#fff", displayName = "Ada", id = 1, pinRequired = false, pinSet = false, slug = "ada")

    @Test
    fun kidCardIsAButtonNamedAfterTheKid() {
        rule.setContent {
            KidCard(KidOverview(kid, done = 1, total = 3, balance = null, streak = null, pendingReviews = 0), onClick = {})
        }
        rule.onNode(hasClickAction())
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .assert(
                SemanticsMatcher("text contains the kid's name") {
                    it.config.getOrNull(SemanticsProperties.Text).orEmpty().any { t -> "Ada" in t.text }
                },
            )
    }

    @Test
    fun choreRowActionNamesTheChore() {
        val instance = ChoreInstance(
            choreId = 7, choreName = "Dishes", date = LocalDate.of(2026, 5, 4),
            dueAt = OffsetDateTime.parse("2026-05-04T20:00:00Z"), id = 7, planned = false,
            status = ChoreInstance.Status.OPEN, kidId = 1,
        )
        rule.setContent { ChoreRow(instance, working = false, onAction = {}) }
        rule.onNode(
            SemanticsMatcher("content description names the chore") {
                it.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty().any { d -> "Dishes" in d }
            } and hasClickAction(),
        ).assertExists()
    }

    @Test
    fun sectionHeaderIsAHeading() {
        rule.setContent { SectionHeader("Today") }
        rule.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.Heading)).assertExists()
    }

    @Test
    fun settingsClickablesAreNamedAndThemeRowsAreRadioButtons() {
        rule.setContent {
            SettingsScreen(appearance = Appearance(), onTheme = {}, onDynamicColor = {}, onBack = {})
        }
        val nodes = rule.onAllNodes(hasClickAction()).fetchSemanticsNodes()
        assertTrue(nodes.isNotEmpty())
        for (node in nodes) {
            val texts = node.config.getOrNull(SemanticsProperties.Text).orEmpty()
            val descriptions = node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
            assertTrue("clickable node without a name: ${node.config}", texts.isNotEmpty() || descriptions.isNotEmpty())
        }
        assertEquals(3, nodes.count { it.config.getOrNull(SemanticsProperties.Role) == Role.RadioButton })
    }
}
