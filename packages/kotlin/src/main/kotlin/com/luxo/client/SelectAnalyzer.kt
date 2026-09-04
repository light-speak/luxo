package com.luxo.client

import java.io.File
import kotlinx.serialization.json.Json
import org.jetbrains.kotlin.cli.common.messages.MessageCollector
import org.jetbrains.kotlin.cli.jvm.compiler.EnvironmentConfigFiles
import org.jetbrains.kotlin.cli.jvm.compiler.KotlinCoreEnvironment
import org.jetbrains.kotlin.com.intellij.openapi.util.Disposer
import org.jetbrains.kotlin.com.intellij.psi.PsiElement
import org.jetbrains.kotlin.com.intellij.psi.PsiErrorElement
import org.jetbrains.kotlin.com.intellij.psi.util.PsiTreeUtil
import org.jetbrains.kotlin.config.CommonConfigurationKeys
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.psi.KtBlockExpression
import org.jetbrains.kotlin.psi.KtCallExpression
import org.jetbrains.kotlin.psi.KtExpression
import org.jetbrains.kotlin.psi.KtFile
import org.jetbrains.kotlin.psi.KtLambdaExpression
import org.jetbrains.kotlin.psi.KtNameReferenceExpression
import org.jetbrains.kotlin.psi.KtNamedFunction
import org.jetbrains.kotlin.psi.KtParenthesizedExpression
import org.jetbrains.kotlin.psi.KtProperty
import org.jetbrains.kotlin.psi.KtPsiFactory
import org.jetbrains.kotlin.psi.KtQualifiedExpression
import org.jetbrains.kotlin.psi.psiUtil.collectDescendantsOfType

/**
 * Kotlin syntax-tree field analyzer.
 *
 * API names, return types, and nested field types always come from the Luxo
 * schema. Source text is parsed with the Kotlin compiler, so comments, string
 * literals, multiline calls, and lexical shadowing cannot create false hints.
 */
object SelectAnalyzer {
    fun analyze(srcDir: String, outFile: String, packageName: String, schema: LuxoSchema) {
        val root = File(srcDir)
        val sources = root.walkTopDown()
            .filter { it.isFile && it.extension == "kt" }
            .filterNot { it.hasExcludedPathSegment(root) }
            .map { it.readText() }
        writeHints(outFile, packageName, analyzeSources(sources, schema))
    }

    internal fun analyzeSources(sources: Sequence<String>, schema: LuxoSchema): Map<String, String> {
        if (schema.apis.isEmpty()) return emptyMap()
        val trees = mutableMapOf<String, SelectionNode>()
        KotlinSyntaxParser().use { parser ->
            sources.forEachIndexed { index, source ->
                val file = parser.parse("LuxoSource$index.kt", source)
                requireValidSyntax(file)
                analyzeFile(file, schema, trees)
            }
        }
        return trees.mapValues { (_, tree) -> renderSelection(tree.children) }.toSortedMap()
    }

    private fun analyzeFile(file: KtFile, schema: LuxoSchema, trees: MutableMap<String, SelectionNode>) {
        for (call in file.collectDescendantsOfType<KtCallExpression>()) {
            val apiName = call.calleeExpression?.text ?: continue
            val api = schema.apis[apiName] ?: continue
            val typeName = api.returnType ?: continue
            if (!schema.isStructured(typeName)) continue

            val tree = trees.getOrPut(apiName) { SelectionNode() }
            val binding = SelectionBinding(typeName, tree, api.returnList, api.paginated)
            val callValue = qualifiedCallValue(call)
            followSelection(callValue, binding, schema)
            analyzeAssignedProperty(callValue, binding, schema)
        }
    }

    private fun analyzeAssignedProperty(
        callValue: KtExpression,
        binding: SelectionBinding,
        schema: LuxoSchema,
    ) {
        val property = callValue.parent as? KtProperty ?: return
        if (unwrap(property.initializer) !== callValue) return
        val name = property.name ?: return
        val scope = property.parent ?: return
        val references = scope.collectDescendantsOfType<KtNameReferenceExpression>()
        for (reference in references) {
            if (reference.getReferencedName() != name) continue
            if (reference.textRange.startOffset < property.textRange.endOffset) continue
            if (visibleProperty(reference, name) !== property) continue
            followSelection(reference, binding, schema)
        }
    }

    private fun followSelection(expression: KtExpression, binding: SelectionBinding, schema: LuxoSchema) {
        val qualified = expression.parent as? KtQualifiedExpression ?: return
        if (qualified.receiverExpression !== expression) return
        when (val selector = qualified.selectorExpression) {
            is KtNameReferenceExpression -> followField(qualified, selector, binding, schema)
            is KtCallExpression -> followCall(qualified, selector, binding, schema)
        }
    }

    private fun followField(
        qualified: KtQualifiedExpression,
        fieldReference: KtNameReferenceExpression,
        binding: SelectionBinding,
        schema: LuxoSchema,
    ) {
        val fieldName = fieldReference.getReferencedName()
        if (binding.paginated) {
            if (fieldName == "items") {
                followSelection(qualified, binding.copy(isList = true, paginated = false), schema)
            }
            return
        }

        val field = schema.field(binding.typeName, fieldName) ?: return
        val node = binding.node.children.getOrPut(fieldName) { SelectionNode() }
        val childType = field.typeName ?: field.type
        followSelection(qualified, SelectionBinding(childType, node, field.isList, false), schema)
    }

    private fun followCall(
        qualified: KtQualifiedExpression,
        call: KtCallExpression,
        binding: SelectionBinding,
        schema: LuxoSchema,
    ) {
        when (call.calleeExpression?.text) {
            "value" -> followSelection(qualified, binding, schema)
            in collectionLambdaMethods -> {
                analyzeLambda(call, binding.copy(isList = false, paginated = false), schema)
                followSelection(qualified, binding, schema)
            }
            in objectLambdaMethods -> {
                analyzeLambda(call, binding.copy(paginated = false), schema)
                followSelection(qualified, binding, schema)
            }
            in collectionElementMethods -> {
                if (binding.isList) {
                    followSelection(qualified, binding.copy(isList = false), schema)
                }
            }
        }
    }

    private fun analyzeLambda(call: KtCallExpression, binding: SelectionBinding, schema: LuxoSchema) {
        val lambda = call.lambdaArguments.firstOrNull()?.getLambdaExpression()
            ?: call.valueArguments.lastOrNull()?.getArgumentExpression() as? KtLambdaExpression
            ?: return
        val parameter = lambda.valueParameters.singleOrNull()?.name ?: "it"
        val body = lambda.bodyExpression ?: return
        for (reference in body.collectDescendantsOfType<KtNameReferenceExpression>()) {
            if (reference.getReferencedName() != parameter) continue
            if (!belongsToLambda(reference, lambda, parameter)) continue
            followSelection(reference, binding, schema)
        }
    }

    private fun belongsToLambda(
        reference: KtNameReferenceExpression,
        target: KtLambdaExpression,
        parameter: String,
    ): Boolean {
        var current: PsiElement? = reference.parent
        while (current != null) {
            if (current is KtLambdaExpression && current.bindsParameter(parameter)) {
                return current === target
            }
            current = current.parent
        }
        return false
    }

    private fun visibleProperty(reference: KtNameReferenceExpression, name: String): KtProperty? {
        var current: PsiElement? = reference.parent
        while (current != null) {
            if (current is KtLambdaExpression && current.bindsParameter(name)) return null
            if (current is KtNamedFunction && current.valueParameters.any { it.name == name }) return null
            val declaration = precedingProperty(current, reference, name)
            if (declaration != null) return declaration
            current = current.parent
        }
        return null
    }

    private fun precedingProperty(
        scope: PsiElement,
        reference: KtNameReferenceExpression,
        name: String,
    ): KtProperty? {
        val properties = when (scope) {
            is KtBlockExpression -> scope.statements.filterIsInstance<KtProperty>()
            is KtFile -> scope.declarations.filterIsInstance<KtProperty>()
            else -> return null
        }
        return properties
            .asSequence()
            .filter { it.name == name && it.textRange.endOffset <= reference.textRange.startOffset }
            .maxByOrNull { it.textRange.startOffset }
    }

    private fun requireValidSyntax(file: KtFile) {
        val errors = PsiTreeUtil.collectElementsOfType(file, PsiErrorElement::class.java)
        require(errors.isEmpty()) {
            val first = errors.first()
            "invalid Kotlin source at offset ${first.textOffset}: ${first.errorDescription}"
        }
    }

    private fun writeHints(outFile: String, packageName: String, hints: Map<String, String>) {
        val target = File(outFile)
        target.parentFile?.mkdirs()
        target.writeText(LuxoCodegen.genSelectHints(packageName, hints))
        println("[luxo] SelectHints -> $outFile (${hints.size} APIs analyzed)")
    }
}

@OptIn(org.jetbrains.kotlin.K1Deprecation::class, CompilerConfiguration.Internals::class)
private class KotlinSyntaxParser : AutoCloseable {
    private val disposable = Disposer.newDisposable("luxo-select-analyzer")
    private val environment: KotlinCoreEnvironment

    init {
        val configuration = CompilerConfiguration().apply {
            put(CommonConfigurationKeys.MODULE_NAME, "luxo-select-analyzer")
            put(CommonConfigurationKeys.MESSAGE_COLLECTOR_KEY, MessageCollector.NONE)
        }
        environment = KotlinCoreEnvironment.createForProduction(
            disposable,
            configuration,
            EnvironmentConfigFiles.JVM_CONFIG_FILES,
        )
    }

    fun parse(fileName: String, source: String): KtFile =
        KtPsiFactory(environment.project, false).createFile(fileName, source)

    override fun close() {
        Disposer.dispose(disposable)
    }
}

private data class SelectionBinding(
    val typeName: String,
    val node: SelectionNode,
    val isList: Boolean,
    val paginated: Boolean,
)

private class SelectionNode {
    val children: MutableMap<String, SelectionNode> = mutableMapOf()
}

private fun LuxoSchema.isStructured(typeName: String): Boolean =
    models.containsKey(typeName) || types.containsKey(typeName)

private fun LuxoSchema.field(typeName: String, fieldName: String): LuxoField? {
    val fields = models[typeName]?.fields ?: types[typeName]?.fields ?: return null
    return fields.firstOrNull { it.name == fieldName }
}

private fun KtLambdaExpression.bindsParameter(name: String): Boolean =
    if (valueParameters.isEmpty()) name == "it" else valueParameters.any { it.name == name }

private fun File.hasExcludedPathSegment(root: File): Boolean {
    val relative = relativeToOrNull(root)?.invariantSeparatorsPath ?: return true
    return relative.split('/').any { it == "generated" || it == "test" }
}

private fun qualifiedCallValue(call: KtCallExpression): KtExpression {
    val parent = call.parent
    return if (parent is KtQualifiedExpression && parent.selectorExpression === call) parent else call
}

private fun unwrap(expression: KtExpression?): KtExpression? {
    var current = expression
    while (current is KtParenthesizedExpression) current = current.expression
    return current
}

private fun renderSelection(fields: Map<String, SelectionNode>): String =
    fields.toSortedMap().entries.joinToString(", ") { (name, node) ->
        if (node.children.isEmpty()) name else "$name { ${renderSelection(node.children)} }"
    }

private val collectionLambdaMethods = setOf(
    "all", "any", "associate", "associateBy", "count", "filter", "filterNot",
    "flatMap", "forEach", "groupBy", "map", "mapNotNull", "none", "onEach",
    "sortedBy", "sortedByDescending",
)

private val objectLambdaMethods = setOf("also", "apply", "let", "run", "takeIf", "takeUnless")
private val collectionElementMethods = setOf("elementAt", "first", "firstOrNull", "last", "lastOrNull", "single", "singleOrNull")

/** CLI entry point for the Gradle JavaExec task. */
object SelectAnalyzerCli {
    @JvmStatic
    fun main(args: Array<String>) {
        require(args.size >= 4) { "Usage: SelectAnalyzerCli <srcDir> <outFile> <package> <schemaFile>" }
        val schemaFile = File(args[3])
        require(schemaFile.isFile) {
            "schema file not found: ${schemaFile.path}; run luxoGenerate first or set -Pluxo.schemaFile"
        }
        val schema = Json.decodeFromString<LuxoSchema>(schemaFile.readText())
        SelectAnalyzer.analyze(args[0], args[1], args[2], schema)
    }
}
